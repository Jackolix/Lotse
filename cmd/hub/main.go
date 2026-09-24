// Command hub runs the Lotse server.
//
//	hub               serve (configured with HUB_* environment variables)
//	hub token         create an enrollment token and print install commands
//	hub healthcheck   exit 0 if the local server answers (Docker HEALTHCHECK)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jackolix/Lotse/internal/hub"
	"github.com/Jackolix/Lotse/internal/version"
)

func main() {
	cfg := hub.ConfigFromEnv()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "", "serve":
		err = serve(cfg)
	case "token":
		err = token(cfg, os.Args[2:])
	case "healthcheck":
		err = healthcheck(cfg)
	case "version", "--version":
		fmt.Println("hub", version.Version)
	default:
		fmt.Fprintf(os.Stderr, "usage: %s [serve | token [--ttl 1h] [--allow-shell] | healthcheck | version]\n", os.Args[0])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func serve(cfg hub.Config) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	h, err := hub.New(cfg, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return h.Run(ctx)
}

// token enrolls agents without the web UI, e.g. from provisioning scripts:
// docker exec lotse-hub /app/hub token
func token(cfg hub.Config, args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	ttl := fs.Duration("ttl", time.Hour, "how long the token can enroll new agents")
	allowShell := fs.Bool("allow-shell", false, "include --allow-shell in the printed install commands")
	fs.Parse(args)

	tok, expires, key, err := hub.CreateEnrollToken(cfg, *ttl)
	if err != nil {
		return err
	}
	hubURL := cfg.PublicURL
	if hubURL == "" {
		hubURL = "http://HUB-ADDRESS:8090"
	}
	cmds := hub.InstallCommands(hubURL, key, tok, *allowShell)
	fmt.Printf("Enrollment token (valid until %s):\n  %s\n\nHub key:\n  %s\n\n", expires.Format(time.DateTime), tok, key)
	fmt.Printf("Linux / macOS:\n  %s\n\nWindows (elevated PowerShell):\n  %s\n", cmds["linux"], cmds["windows"])
	if cfg.PublicURL == "" {
		fmt.Println("\nSet HUB_URL to have the real hub address filled in.")
	}
	return nil
}

func healthcheck(cfg hub.Config) error {
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	return nil
}
