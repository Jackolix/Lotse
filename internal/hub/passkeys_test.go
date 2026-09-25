package hub

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// authenticator simulates a passkey provider.
type authenticator struct {
	rpID   string
	credID []byte
	key    crypto.Signer
	alg    int64
	count  uint32
	handle []byte
}

func newAuthenticator(t *testing.T, rpID string, alg int64) *authenticator {
	a := &authenticator{rpID: rpID, credID: make([]byte, 20), alg: alg}
	rand.Read(a.credID)
	switch alg {
	case algES256:
		a.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case algEdDSA:
		_, a.key, _ = ed25519.GenerateKey(rand.Reader)
	default:
		t.Fatalf("unsupported test algorithm %d", alg)
	}
	return a
}

func (a *authenticator) authData(attested bool) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	b := append([]byte{}, rp[:]...)
	flags := byte(flagUserPresent | flagUserVerified)
	if attested {
		flags |= flagAttested
	}
	b = append(b, flags)
	b = binary.BigEndian.AppendUint32(b, a.count)
	if attested {
		b = append(b, make([]byte, 16)...) // AAGUID
		b = binary.BigEndian.AppendUint16(b, uint16(len(a.credID)))
		b = append(b, a.credID...)
		b = append(b, 0xa0) // the COSE key, which the hub does not read
	}
	return b
}

func clientDataJSON(typ, challenge, origin string) []byte {
	raw, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return raw
}

func (a *authenticator) create(challenge, origin string) attestation {
	spki, _ := x509.MarshalPKIXPublicKey(a.key.Public())
	return attestation{
		ID:                b64url.EncodeToString(a.credID),
		ClientData:        b64url.EncodeToString(clientDataJSON("webauthn.create", challenge, origin)),
		AuthenticatorData: b64url.EncodeToString(a.authData(true)),
		PublicKey:         b64url.EncodeToString(spki),
		Algorithm:         a.alg,
	}
}

func (a *authenticator) get(challenge, origin string) assertion {
	cd := clientDataJSON("webauthn.get", challenge, origin)
	ad := a.authData(false)
	cdHash := sha256.Sum256(cd)
	signed := append(append([]byte{}, ad...), cdHash[:]...)
	var sig []byte
	switch k := a.key.(type) {
	case *ecdsa.PrivateKey:
		digest := sha256.Sum256(signed)
		sig, _ = ecdsa.SignASN1(rand.Reader, k, digest[:])
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, signed)
	}
	return assertion{
		ID:                b64url.EncodeToString(a.credID),
		ClientData:        b64url.EncodeToString(cd),
		AuthenticatorData: b64url.EncodeToString(ad),
		Signature:         b64url.EncodeToString(sig),
		UserHandle:        b64url.EncodeToString(a.handle),
	}
}

// browser is an HTTP client that sends the Origin header like a browser does.
type browser struct {
	t      *testing.T
	c      *http.Client
	origin string
}

func newBrowser(t *testing.T, origin, cookie string) *browser {
	jar, _ := cookiejar.New(nil)
	if cookie != "" {
		u, _ := url.Parse(origin)
		jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: cookie}})
	}
	return &browser{t: t, c: &http.Client{Jar: jar}, origin: origin}
}

func (b *browser) post(path string, body any) (int, map[string]any) {
	b.t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, b.origin+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", b.origin)
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPasskeys(t *testing.T) {
	for _, alg := range []int64{algES256, algEdDSA} {
		t.Run(map[int64]string{algES256: "ES256", algEdDSA: "EdDSA"}[alg], func(t *testing.T) {
			h, srv := newTestHub(t)
			origin := srv.URL
			rpID := strings.Split(strings.TrimPrefix(origin, "http://"), ":")[0]
			cookie, u := sessionFor(t, h, "ada", store.RoleAdmin, true)
			me := newBrowser(t, origin, cookie)
			auth := newAuthenticator(t, rpID, alg)

			// Register.
			status, opts := me.post("/api/me/passkeys/options", map[string]string{"purpose": "register"})
			if status != http.StatusOK {
				t.Fatalf("register options = %d %v", status, opts)
			}
			auth.handle, _ = b64url.DecodeString(opts["user"].(map[string]any)["id"].(string))
			cred := auth.create(opts["challenge"].(string), origin)
			if status, body := me.post("/api/me/passkeys", map[string]any{"name": "Laptop", "credential": cred}); status != http.StatusOK {
				t.Fatalf("register = %d %v", status, body)
			}
			// The same challenge cannot register twice.
			if status, _ := me.post("/api/me/passkeys", map[string]any{"name": "Again", "credential": cred}); status != http.StatusBadRequest {
				t.Errorf("replayed registration = %d, want 400", status)
			}

			login := func(mutate func(*assertion)) (int, map[string]any) {
				b := newBrowser(t, origin, "")
				_, opts := b.post("/api/login/passkey-options", nil)
				a := auth.get(opts["challenge"].(string), origin)
				if mutate != nil {
					mutate(&a)
				}
				return b.post("/api/login", map[string]any{"passkey": a})
			}
			status, body := login(nil)
			if status != http.StatusOK || body["username"] != "ada" || body["passkeys"] != 1.0 {
				t.Fatalf("passkey login = %d %v", status, body)
			}

			// Tampering is refused: another origin, a forged signature, another account.
			foreign := newAuthenticator(t, rpID, alg)
			for name, mutate := range map[string]func(*assertion){
				"origin": func(a *assertion) {
					cd, _ := b64url.DecodeString(a.ClientData)
					a.ClientData = b64url.EncodeToString([]byte(strings.Replace(string(cd), origin, "https://evil.example", 1)))
				},
				"signature": func(a *assertion) {
					*a = foreign.get(challengeOf(t, a), origin)
					a.ID = b64url.EncodeToString(auth.credID)
				},
				"user handle": func(a *assertion) { a.UserHandle = b64url.EncodeToString([]byte("someone else")) },
			} {
				if status, body := login(mutate); status != http.StatusUnauthorized {
					t.Errorf("%s: login = %d %v, want 401", name, status, body)
				}
			}

			// A signature counter must not go backwards.
			auth.count = 5
			if status, _ := login(nil); status != http.StatusOK {
				t.Fatalf("login with counter 5 = %d", status)
			}
			auth.count = 3
			if status, _ := login(nil); status != http.StatusUnauthorized {
				t.Errorf("login with a lower counter = %d, want 401", status)
			}
			auth.count = 6

			// Confirming an action with the passkey instead of the password.
			plain, _ := sessionFor(t, h, "ada", store.RoleAdmin, false)
			b := newBrowser(t, origin, plain)
			_, opts = b.post("/api/me/passkeys/options", map[string]string{"purpose": "elevate"})
			if status, body := b.post("/api/elevate", map[string]any{"passkey": auth.get(opts["challenge"].(string), origin)}); status != http.StatusOK || body["elevated_until"] == nil {
				t.Fatalf("elevate with passkey = %d %v", status, body)
			}
			// A challenge of one session does not work in another.
			_, opts = b.post("/api/me/passkeys/options", map[string]string{"purpose": "elevate"})
			if status, _ := me.post("/api/elevate", map[string]any{"passkey": auth.get(opts["challenge"].(string), origin)}); status != http.StatusForbidden {
				t.Errorf("elevate with another session's challenge = %d, want 403", status)
			}

			keys, _ := h.store.Passkeys(u.ID)
			if len(keys) != 1 || keys[0].Name != "Laptop" || keys[0].SignCount != 6 {
				t.Errorf("stored passkeys: %+v", keys)
			}
		})
	}
}

func challengeOf(t *testing.T, a *assertion) string {
	raw, _ := b64url.DecodeString(a.ClientData)
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		t.Fatal(err)
	}
	return cd.Challenge
}
