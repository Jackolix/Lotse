// Command lotse-agent reports host metrics to the hub.
//
//	lotse-agent install --hub URL --key KEY --token TOKEN   install and start the system service
//	lotse-agent run [--config PATH]                         run in the foreground (used by the service)
//	lotse-agent uninstall [--purge]                         remove the service
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/kardianos/service"
	"golang.org/x/crypto/ssh"

	"github.com/Jackolix/Lotse/internal/agent"
	"github.com/Jackolix/Lotse/internal/sshkey"
	"github.com/Jackolix/Lotse/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "install":
		err = cmdInstall(os.Args[2:])
	case "uninstall":
		err = cmdUninstall(os.Args[2:])
	case "version", "--version":
		fmt.Println(version.AgentName, version.Version)
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %[1]s install --hub URL --key KEY --token TOKEN [--allow-shell] [--no-updates]
                                                  install and start the system service
  %[1]s run [--config PATH]                         run in the foreground
  %[1]s uninstall [--purge]                         stop and remove the service
  %[1]s version
`, version.AgentName)
}

// Custom unit: kardianos' default waits 120s before restarting a crashed service.
const systemdUnit = `[Unit]
Description={{Description}}
ConditionFileIsExecutable={{Path | cmdEscape}}
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{Path | cmdEscape}}{{range Arguments}} {{. | cmd}}{{end}}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`

func serviceConfig(cfgPath string) *service.Config {
	return &service.Config{
		Name:        version.AgentName,
		DisplayName: "Lotse Agent",
		Description: "Reports system metrics to the Lotse hub.",
		Arguments:   []string{"run", "--config", cfgPath},
		Option: service.KeyValue{
			"SystemdScript":          systemdUnit,
			"RunAtLoad":              true, // launchd: start at boot
			"KeepAlive":              true,
			"OnFailure":              "restart", // Windows
			"OnFailureDelayDuration": "5s",
		},
	}
}

type program struct {
	cfgPath string
	log     *slog.Logger
	cancel  context.CancelFunc
	done    chan struct{}
}

func (p *program) Start(service.Service) error {
	cfg, err := agent.LoadConfig(p.cfgPath)
	if err != nil {
		p.log.Error("cannot load config", "path", p.cfgPath, "err", err)
		return err
	}
	a, err := agent.New(cfg, p.log)
	if err != nil {
		p.log.Error("cannot start agent", "err", err)
		return err
	}
	a.Restart = func(exe string) { restart(exe, p.log) }
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	go func() {
		defer close(p.done)
		a.Run(ctx)
	}()
	return nil
}

func (p *program) Stop(service.Service) error {
	if p.cancel == nil {
		return nil
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
	fs.Parse(args)

	prg := &program{cfgPath: *cfgPath, log: newLogger(*cfgPath)}
	s, err := service.New(prg, serviceConfig(*cfgPath))
	if err != nil {
		return err
	}
	return s.Run()
}

func cmdInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	hub := fs.String("hub", "", "hub URL, e.g. http://hub.lan:8090")
	key := fs.String("key", "", "hub public key (ssh-ed25519 ...)")
	token := fs.String("token", "", "enrollment token from the hub's \"Add system\" dialog")
	allowShell := fs.Bool("allow-shell", false, "let hub users control this machine: shell, files and scripts as root/SYSTEM, processes, services, reboot")
	noUpdates := fs.Bool("no-updates", false, "refuse self-updates from the hub, even signed ones")
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
	fs.Parse(args)

	if err := requireAdmin(); err != nil {
		return err
	}
	cfg, err := agent.NewConfig(*cfgPath, *hub, *key, *token, *allowShell)
	if err != nil {
		return err
	}
	// The folder is locked down to root/SYSTEM and deleted by uninstall --purge, so
	// it must belong to the agent alone.
	if ok, err := cfg.DedicatedDir(); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%s holds other files; give the agent's config a folder of its own, e.g. %s",
			filepath.Dir(cfg.Path()), agent.DefaultConfigPath())
	}
	cfg.DisableUpdates = *noUpdates
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	// Create the agent identity now so the fingerprint can be shown.
	signer, err := sshkey.LoadOrCreate(cfg.KeyPath())
	if err != nil {
		return fmt.Errorf("create agent key: %w", err)
	}

	s, err := service.New(&program{}, serviceConfig(*cfgPath))
	if err != nil {
		return err
	}
	// Replace an existing service. Only fail on removal if we know one is installed;
	// some service managers report other errors for "not there".
	if _, statusErr := s.Status(); !errors.Is(statusErr, service.ErrNotInstalled) {
		_ = s.Stop()
		if err := s.Uninstall(); err != nil && statusErr == nil {
			return fmt.Errorf("remove previous service: %w", err)
		}
	}
	if err := s.Install(); err != nil {
		return fmt.Errorf("install service: %w", err)
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	shell := "disabled (reinstall with --allow-shell to enable)"
	if cfg.AllowShell {
		shell = "enabled"
	}
	updates := "signed updates from the hub"
	if cfg.DisableUpdates {
		updates = "disabled"
	}
	fmt.Printf("%s %s installed and running.\n  config:         %s\n  fingerprint:    %s\n  remote control: %s\n  updates:        %s\n",
		version.AgentName, version.Version, cfg.Path(), ssh.FingerprintSHA256(signer.PublicKey()), shell, updates)
	return nil
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete the config and agent key")
	cfgPath := fs.String("config", agent.DefaultConfigPath(), "config file")
	fs.Parse(args)

	if err := requireAdmin(); err != nil {
		return err
	}
	s, err := service.New(&program{}, serviceConfig(*cfgPath))
	if err != nil {
		return err
	}
	_ = s.Stop()
	if err := s.Uninstall(); err != nil {
		if _, statusErr := s.Status(); !errors.Is(statusErr, service.ErrNotInstalled) {
			return fmt.Errorf("remove service: %w", err)
		}
	}
	if *purge {
		if err := agent.PurgeConfig(*cfgPath); err != nil {
			return err
		}
	}
	exe, _ := os.Executable()
	fmt.Printf("Service removed. You can now delete %s\n", exe)
	return nil
}

// newLogger writes to stderr, which ends up in journald on Linux. launchd and the
// Windows service manager discard stderr, so there the agent logs to a file.
func newLogger(cfgPath string) *slog.Logger {
	var w io.Writer = os.Stderr
	if runtime.GOOS != "linux" && !service.Interactive() {
		path := filepath.Join(filepath.Dir(cfgPath), "agent.log")
		if runtime.GOOS == "darwin" {
			path = filepath.Join("/Library/Logs", version.AgentName+".log")
		}
		if f, err := openLogFile(path); err == nil {
			w = f
		}
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

// maxLogSize is when the log file is rotated; one previous file is kept.
const maxLogSize = 5 << 20

// logFile is an append-only log that rotates itself, since the agent may run for
// months without a restart.
type logFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openLogFile(path string) (*logFile, error) {
	l := &logFile{path: path}
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
		_ = os.Rename(path, path+".1")
	}
	return l, l.open()
}

func (l *logFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.f = f
	if fi, err := f.Stat(); err == nil {
		l.size = fi.Size()
	}
	return nil
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > maxLogSize {
		l.f.Close()
		_ = os.Rename(l.path, l.path+".1")
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}
