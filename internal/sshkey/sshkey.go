// Package sshkey loads or creates the Ed25519 identities of the hub and agents.
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// LoadOrCreate reads an OpenSSH private key from path, generating an Ed25519 key if the file is missing.
func LoadOrCreate(path string) (ssh.Signer, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return ssh.ParsePrivateKey(data)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return ssh.NewSignerFromKey(priv)
}

// AuthorizedKey formats a public key as a single authorized_keys line without trailing newline.
func AuthorizedKey(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

// ParseAuthorizedKey parses a single "ssh-ed25519 AAAA..." line.
func ParseAuthorizedKey(s string) (ssh.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(s)))
	if err != nil {
		return nil, err
	}
	if key.Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("key must be ssh-ed25519")
	}
	return key, nil
}
