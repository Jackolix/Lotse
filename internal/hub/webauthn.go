package hub

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// A minimal WebAuthn relying party (https://www.w3.org/TR/webauthn-3/) for passkeys.
// Browsers hand over the credential's public key as SubjectPublicKeyInfo
// (AuthenticatorAttestationResponse.getPublicKey), so no CBOR parsing is needed.
// Attestation is not requested: which authenticator holds a passkey does not matter
// here, only that it proves possession and user verification on every use.

// COSE algorithm identifiers the hub accepts.
const (
	algES256 = -7
	algEdDSA = -8
	algRS256 = -257
)

// Authenticator data flags.
const (
	flagUserPresent  = 0x01
	flagUserVerified = 0x04
	flagAttested     = 0x40
)

const (
	passkeyTimeout = 5 * time.Minute
	maxChallenges  = 1000
)

var b64url = base64.RawURLEncoding

// assertion is a navigator.credentials.get() result, fields base64url encoded.
type assertion struct {
	ID                string `json:"id"`
	ClientData        string `json:"client_data"`
	AuthenticatorData string `json:"authenticator_data"`
	Signature         string `json:"signature"`
	UserHandle        string `json:"user_handle"`
}

// attestation is a navigator.credentials.create() result, fields base64url encoded.
type attestation struct {
	ID                string `json:"id"`
	ClientData        string `json:"client_data"`
	AuthenticatorData string `json:"authenticator_data"`
	PublicKey         string `json:"public_key"` // SubjectPublicKeyInfo
	Algorithm         int64  `json:"algorithm"`
}

// ---- challenges ----

type challenge struct {
	purpose string // login, elevate, register
	session []byte // browser session for elevate and register
	expires time.Time
}

// challenges are single-use and expire after passkeyTimeout.
type challenges struct {
	mu sync.Mutex
	m  map[string]challenge
}

// add stores a new challenge. When too many are pending, expired ones go first,
// then arbitrary ones: someone flooding the endpoint only makes others retry,
// instead of locking everyone out.
func (c *challenges) add(purpose string, session []byte) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]challenge{}
	}
	now := time.Now()
	if len(c.m) >= maxChallenges {
		for k, ch := range c.m {
			if now.After(ch.expires) {
				delete(c.m, k)
			}
		}
		for k := range c.m { // map order is random
			if len(c.m) < maxChallenges {
				break
			}
			delete(c.m, k)
		}
	}
	b := make([]byte, 32)
	rand.Read(b)
	key := b64url.EncodeToString(b)
	c.m[key] = challenge{purpose: purpose, session: session, expires: now.Add(passkeyTimeout)}
	return key
}

// take consumes a challenge. It fails for unknown, expired or foreign challenges.
func (c *challenges) take(key, purpose string, session []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.m[key]
	if !ok {
		return false
	}
	delete(c.m, key)
	return ch.purpose == purpose && bytes.Equal(ch.session, session) && time.Now().Before(ch.expires)
}

// ---- relying party ----

// relyingParty derives origin and RP ID from the browser's Origin header, which the
// sameOrigin middleware has matched against the Host. Passkeys are bound to the
// hostname the hub is opened with.
func relyingParty(r *http.Request) (origin, rpID string, err error) {
	origin = r.Header.Get("Origin")
	u, perr := url.Parse(origin)
	if origin == "" || perr != nil || u.Host != r.Host {
		return "", "", errors.New("passkeys only work from the hub's own page")
	}
	return origin, u.Hostname(), nil
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

// checkClientData verifies what the browser says it signed: the ceremony type, one
// of our challenges and our origin.
func (h *Hub) checkClientData(raw []byte, typ, origin, purpose string, session []byte) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return errors.New("invalid passkey response")
	}
	// Taken first, so a challenge is used up even by a response that fails below.
	if !h.challenges.take(cd.Challenge, purpose, session) {
		return errors.New("the passkey request expired, try again")
	}
	if cd.Type != typ || cd.CrossOrigin {
		return errors.New("invalid passkey response")
	}
	if cd.Origin != origin {
		return errors.New("the passkey response is for another site")
	}
	return nil
}

type authData struct {
	flags        byte
	signCount    uint32
	credentialID []byte // only in registrations
}

// parseAuthData reads authenticator data (WebAuthn section 6.1) and checks that it
// is for rpID and that the user was present and verified (PIN, biometrics).
func parseAuthData(b []byte, rpID string) (*authData, error) {
	if len(b) < 37 {
		return nil, errors.New("invalid authenticator data")
	}
	want := sha256.Sum256([]byte(rpID))
	if !bytes.Equal(b[:32], want[:]) {
		return nil, errors.New("the passkey belongs to another site")
	}
	ad := &authData{flags: b[32], signCount: binary.BigEndian.Uint32(b[33:37])}
	if ad.flags&flagUserPresent == 0 || ad.flags&flagUserVerified == 0 {
		return nil, errors.New("the authenticator did not verify you (PIN or biometrics)")
	}
	if ad.flags&flagAttested != 0 {
		// AAGUID (16 bytes), credential ID length (2), credential ID, public key (CBOR).
		if len(b) < 55 {
			return nil, errors.New("invalid authenticator data")
		}
		n := int(binary.BigEndian.Uint16(b[53:55]))
		if n == 0 || len(b) < 55+n {
			return nil, errors.New("invalid authenticator data")
		}
		ad.credentialID = b[55 : 55+n]
	}
	return ad, nil
}

// parsePublicKey checks that a registered key matches its algorithm.
func parsePublicKey(spki []byte, alg int64) error {
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return errors.New("invalid passkey public key")
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if alg == algES256 && k.Curve == elliptic.P256() {
			return nil
		}
	case ed25519.PublicKey:
		if alg == algEdDSA {
			return nil
		}
	case *rsa.PublicKey:
		if alg == algRS256 && k.N.BitLen() >= 2048 {
			return nil
		}
	}
	return errors.New("this passkey uses an unsupported algorithm")
}

// verifySignature checks an assertion signature over authData || SHA-256(clientData).
func verifySignature(spki []byte, alg int64, authenticatorData, clientDataJSON, sig []byte) bool {
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		return false
	}
	cdHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte{}, authenticatorData...), cdHash[:]...)
	digest := sha256.Sum256(signed)
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return alg == algES256 && ecdsa.VerifyASN1(k, digest[:], sig)
	case ed25519.PublicKey:
		return alg == algEdDSA && ed25519.Verify(k, signed, sig)
	case *rsa.PublicKey:
		return alg == algRS256 && rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil
	}
	return false
}

func decodeFields(fields ...string) ([][]byte, error) {
	out := make([][]byte, len(fields))
	for i, f := range fields {
		b, err := b64url.DecodeString(f)
		if err != nil {
			return nil, errors.New("invalid passkey response")
		}
		out[i] = b
	}
	return out, nil
}
