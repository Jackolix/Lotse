package hub

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// Passkeys sign in without password and two-factor code: the authenticator already
// checks possession and a PIN or biometrics. They also confirm sensitive actions.

type credentialDescriptor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (h *Hub) descriptors(userID int64, rpID string) ([]credentialDescriptor, error) {
	keys, err := h.store.Passkeys(userID)
	if err != nil {
		return nil, err
	}
	out := []credentialDescriptor{}
	for _, k := range keys {
		if k.RPID == rpID {
			out = append(out, credentialDescriptor{"public-key", b64url.EncodeToString(k.CredentialID)})
		}
	}
	return out, nil
}

// requestOptions is PublicKeyCredentialRequestOptions for navigator.credentials.get.
func requestOptions(challenge, rpID string, allow []credentialDescriptor) map[string]any {
	opts := map[string]any{
		"challenge":        challenge,
		"rpId":             rpID,
		"timeout":          passkeyTimeout.Milliseconds(),
		"userVerification": "required",
	}
	if allow != nil {
		opts["allowCredentials"] = allow
	}
	return opts
}

// postLoginPasskeyOptions starts a passkey sign-in. The browser lets the user pick
// any passkey it has for this site (discoverable credentials).
func (h *Hub) postLoginPasskeyOptions(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again in a few minutes")
		return
	}
	_, rpID, err := relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, requestOptions(h.challenges.add("login", nil), rpID, nil))
}

// postPasskeyOptions starts adding a passkey ("register") or confirming an action
// with one ("elevate") for the signed-in user.
func (h *Hub) postPasskeyOptions(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body struct {
		Purpose string `json:"purpose"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	_, rpID, err := relyingParty(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := h.descriptors(s.ID, rpID)
	if err != nil {
		h.internalError(w, err)
		return
	}
	switch body.Purpose {
	case "elevate":
		if len(existing) == 0 {
			writeError(w, http.StatusConflict, "you have no passkey for this address")
			return
		}
		writeJSON(w, http.StatusOK, requestOptions(h.challenges.add("elevate", s.TokenHash), rpID, existing))
	case "register":
		if !requireElevated(w, s) {
			return
		}
		handle, err := h.store.PasskeyHandle(s.ID)
		if err != nil {
			h.internalError(w, err)
			return
		}
		ch := h.challenges.add("register", s.TokenHash)
		params := []map[string]any{}
		for _, alg := range []int{algEdDSA, algES256, algRS256} {
			params = append(params, map[string]any{"type": "public-key", "alg": alg})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"challenge":          ch,
			"rp":                 map[string]string{"id": rpID, "name": "Lotse"},
			"user":               map[string]string{"id": b64url.EncodeToString(handle), "name": s.Username, "displayName": s.Username},
			"pubKeyCredParams":   params,
			"timeout":            passkeyTimeout.Milliseconds(),
			"excludeCredentials": existing,
			"authenticatorSelection": map[string]any{
				"residentKey": "required", "requireResidentKey": true, "userVerification": "required",
			},
			"attestation": "none",
		})
	default:
		writeError(w, http.StatusBadRequest, "purpose must be register or elevate")
	}
}

// verifyAssertion checks a passkey response and returns the passkey it came from.
// Errors are safe to show to the user.
func (h *Hub) verifyAssertion(r *http.Request, a *assertion, purpose string, session []byte) (*store.Passkey, error) {
	origin, rpID, err := relyingParty(r)
	if err != nil {
		return nil, err
	}
	raw, err := decodeFields(a.ID, a.ClientData, a.AuthenticatorData, a.Signature, a.UserHandle)
	if err != nil {
		return nil, err
	}
	credID, cdJSON, adBytes, sig, userHandle := raw[0], raw[1], raw[2], raw[3], raw[4]
	if err := h.checkClientData(cdJSON, "webauthn.get", origin, purpose, session); err != nil {
		return nil, err
	}
	ad, err := parseAuthData(adBytes, rpID)
	if err != nil {
		return nil, err
	}
	key, err := h.store.PasskeyByCredential(credID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, errors.New("this passkey is not registered with the hub")
	}
	if err != nil {
		return nil, err
	}
	if key.RPID != rpID || !verifySignature(key.PublicKey, key.Algorithm, adBytes, cdJSON, sig) {
		return nil, errors.New("the passkey signature is invalid")
	}
	if len(userHandle) > 0 {
		handle, err := h.store.PasskeyHandle(key.UserID)
		if err != nil || !bytes.Equal(handle, userHandle) {
			return nil, errors.New("the passkey belongs to another account")
		}
	}
	// Authenticators that count signatures must count up; a repeat means a cloned key.
	// Most passkey providers always send 0.
	if (ad.signCount != 0 || key.SignCount != 0) && ad.signCount <= key.SignCount {
		return nil, errors.New("this passkey's signature counter went backwards; it may have been copied")
	}
	if err := h.store.UsePasskey(key.ID, ad.signCount); err != nil {
		h.log.Error("recording passkey use failed", "err", err)
	}
	return key, nil
}

func (h *Hub) loginWithPasskey(w http.ResponseWriter, r *http.Request, a *assertion) {
	ip := clientIP(r)
	if !h.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again in a few minutes")
		return
	}
	key, err := h.verifyAssertion(r, a, "login", nil)
	if err != nil {
		h.limiter.fail(ip)
		h.audit(r, "", "login_failed", nil, "passkey: "+err.Error())
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	u, err := h.store.User(key.UserID)
	if err != nil {
		h.internalError(w, err)
		return
	}
	h.limiter.reset(ip)
	h.audit(r, u.Username, "login", nil, fmt.Sprintf("with passkey %q", key.Name))
	h.startSession(w, r, u)
}

// reauthenticateWithPasskey confirms the session's user with one of their passkeys
// and returns its name, or "" after writing an error.
func (h *Hub) reauthenticateWithPasskey(w http.ResponseWriter, r *http.Request, s *store.Session, a *assertion) string {
	ip := clientIP(r)
	if !h.limiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "too many failed attempts, try again in a few minutes")
		return ""
	}
	key, err := h.verifyAssertion(r, a, "elevate", s.TokenHash)
	if err == nil && key.UserID != s.ID {
		err = errors.New("that passkey belongs to another account")
	}
	if err != nil {
		h.limiter.fail(ip)
		h.audit(r, s.Username, "reauth_failed", nil, "passkey: "+err.Error())
		writeError(w, http.StatusForbidden, err.Error())
		return ""
	}
	h.limiter.reset(ip)
	return key.Name
}

// ---- managing passkeys ----

func (h *Hub) getPasskeys(w http.ResponseWriter, _ *http.Request, s *store.Session) {
	keys, err := h.store.Passkeys(s.ID)
	if err != nil {
		h.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func passkeyName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	return name, utf8.RuneCountInString(name) <= maxNameLen
}

// postPasskey stores a new passkey. Like other sign-in changes it needs a recent
// re-authentication.
func (h *Hub) postPasskey(w http.ResponseWriter, r *http.Request, s *store.Session) {
	var body struct {
		Name       string      `json:"name"`
		Credential attestation `json:"credential"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !requireElevated(w, s) {
		return
	}
	name, ok := passkeyName(body.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "name must be at most 64 characters")
		return
	}
	key, err := h.verifyAttestation(r, &body.Credential, s)
	if err != nil {
		h.audit(r, s.Username, "passkey_add_failed", nil, err.Error())
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key.Name = name
	if err := h.store.CreatePasskey(key); err != nil {
		if errors.Is(err, store.ErrPasskeyExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "passkey_added", nil, fmt.Sprintf("%q for %s", key.Name, key.RPID))
	writeJSON(w, http.StatusOK, key)
}

func (h *Hub) verifyAttestation(r *http.Request, a *attestation, s *store.Session) (*store.Passkey, error) {
	origin, rpID, err := relyingParty(r)
	if err != nil {
		return nil, err
	}
	raw, err := decodeFields(a.ID, a.ClientData, a.AuthenticatorData, a.PublicKey)
	if err != nil {
		return nil, err
	}
	credID, cdJSON, adBytes, spki := raw[0], raw[1], raw[2], raw[3]
	if err := h.checkClientData(cdJSON, "webauthn.create", origin, "register", s.TokenHash); err != nil {
		return nil, err
	}
	ad, err := parseAuthData(adBytes, rpID)
	if err != nil {
		return nil, err
	}
	if len(credID) == 0 || len(credID) > 1023 || !bytes.Equal(ad.credentialID, credID) {
		return nil, errors.New("invalid passkey response")
	}
	if len(spki) == 0 {
		return nil, errors.New("this browser did not share the passkey's public key; try a current browser")
	}
	if err := parsePublicKey(spki, a.Algorithm); err != nil {
		return nil, err
	}
	return &store.Passkey{UserID: s.ID, RPID: rpID, CredentialID: credID, PublicKey: spki, Algorithm: a.Algorithm,
		SignCount: ad.signCount}, nil
}

func (h *Hub) patchPasskey(w http.ResponseWriter, r *http.Request, s *store.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	name, ok := passkeyName(body.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "name must be at most 64 characters")
		return
	}
	if err := h.store.RenamePasskey(id, s.ID, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "passkey not found")
			return
		}
		h.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) deletePasskey(w http.ResponseWriter, r *http.Request, s *store.Session) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !requireElevated(w, s) {
		return
	}
	key, err := h.store.DeletePasskey(id, s.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "passkey not found")
		return
	}
	if err != nil {
		h.internalError(w, err)
		return
	}
	h.audit(r, s.Username, "passkey_removed", nil, fmt.Sprintf("%q", key.Name))
	w.WriteHeader(http.StatusNoContent)
}
