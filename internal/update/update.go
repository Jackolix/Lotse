// Package update signs and verifies agent binaries for self-updates.
//
// Every release agent binary gets a manifest (FILE.sig, JSON) with its version, size
// and SHA-256, signed with the release key. Agents carry the public key and only
// install binaries whose manifest verifies, that are built for their platform and
// that are newer than themselves. A compromised hub can therefore neither push its
// own code nor downgrade agents to an older, signed release.
package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Jackolix/Lotse/internal/version"
)

// TrustedKeys are the Ed25519 public keys (base64) whose signatures agents accept.
// The release workflow signs with the matching private key (the LOTSE_SIGNING_KEY
// secret). To sign your own builds, replace this key with one from
// `go run ./cmd/sign keygen`.
var TrustedKeys = []string{
	"lITcC4wbtAxvFw8+vlnsF174FzVYVYm8Qgn9D+0aT64=",
}

// ManifestExt is appended to a binary's file name for its manifest.
const ManifestExt = ".sig"

// signedPrefix separates these signatures from anything else the key might sign.
const signedPrefix = "lotse-agent-update-v1"

// Manifest describes one signed agent binary.
type Manifest struct {
	File      string `json:"file"` // e.g. lotse-agent-linux-amd64
	Version   string `json:"version"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`    // hex
	Signature string `json:"signature"` // base64 Ed25519 signature of message()
}

// AgentFile is the release file name of the agent for a platform.
func AgentFile(goos, goarch string) string {
	name := version.AgentName + "-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// message is what gets signed. None of the fields may contain a newline.
func (m *Manifest) message() ([]byte, error) {
	for _, f := range []string{m.File, m.Version, m.SHA256} {
		if f == "" || strings.ContainsAny(f, "\n\r") {
			return nil, errors.New("manifest has an empty or invalid field")
		}
	}
	if _, ok := parseVersion(m.Version); !ok {
		return nil, fmt.Errorf("version %q is not a release version", m.Version)
	}
	if m.Size <= 0 {
		return nil, errors.New("manifest has no size")
	}
	return fmt.Appendf(nil, "%s\n%s\n%s\n%d\n%s\n", signedPrefix, m.File, m.Version, m.Size, m.SHA256), nil
}

// Verify checks the signature against TrustedKeys.
func (m *Manifest) Verify() error {
	msg, err := m.message()
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("manifest signature is malformed")
	}
	for _, k := range TrustedKeys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			continue
		}
		if ed25519.Verify(pub, msg, sig) {
			return nil
		}
	}
	return errors.New("the update is not signed with a trusted release key")
}

// Sign hashes the file at path and returns its signed manifest.
func Sign(path, version string, key ed25519.PrivateKey) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return nil, err
	}
	m := &Manifest{File: filepath.Base(path), Version: version, Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}
	msg, err := m.message()
	if err != nil {
		return nil, err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, msg))
	return m, nil
}

// ReadManifest loads a manifest file.
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// ---- versions ----

type semver struct {
	core [3]int
	pre  []string // pre-release identifiers; empty for a release
}

// parseVersion accepts MAJOR.MINOR.PATCH with an optional -pre-release suffix and
// an optional leading "v". Build metadata (+...) is ignored.
func parseVersion(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		v.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return semver{}, false
		}
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

func compare(a, b semver) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1 // a release is newer than its pre-releases
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case yerr == nil:
			return 1
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// IsRelease reports whether v is a release version that can be signed.
func IsRelease(v string) bool {
	_, ok := parseVersion(v)
	return ok
}

// Newer reports whether candidate is a release version newer than current.
// Development builds ("dev", "main-abc1234") count as older than every release.
func Newer(candidate, current string) bool {
	c, ok := parseVersion(candidate)
	if !ok {
		return false
	}
	cur, ok := parseVersion(current)
	if !ok {
		return true
	}
	return compare(c, cur) > 0
}
