package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Jackolix/Lotse/internal/sshkey"
	"github.com/Jackolix/Lotse/internal/version"
)

// Config is the agent's on-disk configuration, written by "install".
type Config struct {
	HubURL string `json:"hub_url"`
	HubKey string `json:"hub_key"`         // the hub's public key, authorized_keys format
	Token  string `json:"token,omitempty"` // enrollment token; cleared after the hub accepts this agent

	// AllowShell lets hub admins open a root/SYSTEM shell on this machine. Off by
	// default; only someone with local admin rights can turn it on.
	AllowShell bool `json:"allow_shell"`

	path string
}

// DefaultConfigPath is where "install" writes the config and the service reads it.
func DefaultConfigPath() string {
	var dir string
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		dir = filepath.Join(base, version.AgentName)
	case "darwin":
		dir = filepath.Join("/Library/Application Support", version.AgentName)
	default:
		dir = filepath.Join("/etc", version.AgentName)
	}
	return filepath.Join(dir, "agent.json")
}

// KeyPath is the agent's private key, stored next to its config.
func (c *Config) KeyPath() string {
	return filepath.Join(filepath.Dir(c.path), "agent_ed25519")
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{path: path}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, c.Validate()
}

func NewConfig(path, hubURL, hubKey, token string, allowShell bool) (*Config, error) {
	c := &Config{HubURL: hubURL, HubKey: hubKey, Token: token, AllowShell: allowShell, path: path}
	return c, c.Validate()
}

func (c *Config) Validate() error {
	u, err := url.Parse(c.HubURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("hub URL must look like http(s)://host:port, got %q", c.HubURL)
	}
	if _, err := sshkey.ParseAuthorizedKey(c.HubKey); err != nil {
		return fmt.Errorf("invalid hub key: %w", err)
	}
	return nil
}

// Save writes the config readable only by its owner (root / SYSTEM).
func (c *Config) Save() error {
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := restrictDir(dir); err != nil {
		return fmt.Errorf("restrict permissions on %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Path returns the file the config was loaded from or will be saved to.
func (c *Config) Path() string { return c.path }
