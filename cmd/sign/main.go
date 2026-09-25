// Command sign creates the release signing key and signs agent binaries for
// self-updates. It is a build tool; it is not shipped.
//
//	go run ./cmd/sign keygen                          print a new key pair
//	LOTSE_SIGNING_KEY=... go run ./cmd/sign -version 0.4.0 FILE...
//	                                                   write FILE.sig for every FILE
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Jackolix/Lotse/internal/update"
)

const keyEnv = "LOTSE_SIGNING_KEY"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "keygen" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		fmt.Printf("Private key (store as the %s secret, never commit it):\n%s\n\n", keyEnv, base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Printf("Public key (put into update.TrustedKeys):\n%s\n", base64.StdEncoding.EncodeToString(pub))
		return nil
	}

	fs := flag.NewFlagSet("sign", flag.ExitOnError)
	ver := fs.String("version", "", "release version of the binaries, e.g. 0.4.0")
	optional := fs.Bool("optional", false, "exit quietly when no signing key is set (local builds)")
	fs.Parse(args)

	seed := strings.TrimSpace(os.Getenv(keyEnv))
	if seed == "" {
		if *optional {
			fmt.Println("sign: no signing key set, agents are not signed for self-updates")
			return nil
		}
		return fmt.Errorf("set %s to the base64 private key", keyEnv)
	}
	if !update.IsRelease(*ver) {
		if *optional {
			fmt.Printf("sign: %q is not a release version, agents are not signed\n", *ver)
			return nil
		}
		return fmt.Errorf("-version must be a release version like 1.2.3, got %q", *ver)
	}
	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil || len(raw) != ed25519.SeedSize {
		return errors.New(keyEnv + " is not a base64 Ed25519 private key")
	}
	key := ed25519.NewKeyFromSeed(raw)

	for _, path := range fs.Args() {
		m, err := update.Sign(path, *ver, key)
		if err != nil {
			return err
		}
		if err := m.Verify(); err != nil {
			return fmt.Errorf("the signing key does not match update.TrustedKeys: %w", err)
		}
		data, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(path+update.ManifestExt, append(data, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Printf("signed %s (%s, %d bytes)\n", m.File, m.Version, m.Size)
	}
	return nil
}
