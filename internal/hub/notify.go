package hub

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// notification is one message about an alert, rendered per channel type.
type notification struct {
	Title   string
	Message string
	Firing  bool // false: resolved or test
	URL     string
	Time    time.Time

	System    string
	Metric    string
	Value     float64
	Threshold float64
}

// notifierConfig holds every field any channel type uses; validate checks which
// ones a type needs.
type notifierConfig struct {
	URL      string `json:"url,omitempty"`      // ntfy topic URL, Discord/Slack/webhook URL
	Token    string `json:"token,omitempty"`    // ntfy access token, Telegram bot token (secret)
	ChatID   string `json:"chat_id,omitempty"`  // Telegram
	Host     string `json:"host,omitempty"`     // SMTP
	Port     int    `json:"port,omitempty"`     // SMTP
	Username string `json:"username,omitempty"` // SMTP
	Password string `json:"password,omitempty"` // SMTP (secret)
	From     string `json:"from,omitempty"`     // SMTP
	To       string `json:"to,omitempty"`       // SMTP, comma separated
	TLS      string `json:"tls,omitempty"`      // SMTP: starttls (default), tls, none
}

var notifierTypes = map[string]bool{"ntfy": true, "discord": true, "slack": true, "telegram": true, "webhook": true, "email": true}

// telegramAPI is overridden in tests.
var telegramAPI = "https://api.telegram.org"

var notifyClient = &http.Client{Timeout: 15 * time.Second}

func (c *notifierConfig) validate(typ string) error {
	needURL := func() error {
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("enter a valid http(s) URL")
		}
		return nil
	}
	switch typ {
	case "ntfy", "discord", "slack", "webhook":
		return needURL()
	case "telegram":
		if c.Token == "" || c.ChatID == "" {
			return errors.New("Telegram needs a bot token and a chat ID")
		}
	case "email":
		if c.Host == "" || c.From == "" || c.To == "" {
			return errors.New("email needs a server, a sender and at least one recipient")
		}
		if c.TLS == "" {
			c.TLS = "starttls"
		}
		if c.TLS != "starttls" && c.TLS != "tls" && c.TLS != "none" {
			return errors.New("TLS must be starttls, tls or none")
		}
	default:
		return fmt.Errorf("unknown channel type %q", typ)
	}
	return nil
}

// masked returns the config for the browser: secrets are replaced by a flag.
func (c notifierConfig) masked() map[string]any {
	raw, _ := json.Marshal(c)
	var m map[string]any
	json.Unmarshal(raw, &m)
	for _, k := range []string{"token", "password"} {
		_, set := m[k]
		delete(m, k)
		m[k+"_set"] = set
	}
	return m
}

// keepSecrets fills secrets the browser left empty from the stored config.
func (c *notifierConfig) keepSecrets(stored notifierConfig) {
	if c.Token == "" {
		c.Token = stored.Token
	}
	if c.Password == "" {
		c.Password = stored.Password
	}
}

func send(ctx context.Context, typ string, c notifierConfig, n notification) error {
	switch typ {
	case "ntfy":
		return sendNtfy(ctx, c, n)
	case "discord":
		color := 0x0ca30c
		if n.Firing {
			color = 0xd03b3b
		}
		embed := map[string]any{"title": n.Title, "description": n.Message, "color": color, "timestamp": n.Time.Format(time.RFC3339)}
		if n.URL != "" {
			embed["url"] = n.URL
		}
		return postJSON(ctx, c.URL, map[string]any{"username": "Lotse", "embeds": []any{embed}}, nil)
	case "slack":
		return postJSON(ctx, c.URL, map[string]any{"text": plainText(n)}, nil)
	case "telegram":
		endpoint := telegramAPI + "/bot" + c.Token + "/sendMessage"
		body := map[string]any{"chat_id": c.ChatID, "text": plainText(n), "disable_web_page_preview": true}
		return postJSON(ctx, endpoint, body, nil)
	case "webhook":
		status := "resolved"
		if n.Firing {
			status = "firing"
		}
		return postJSON(ctx, c.URL, map[string]any{
			"status": status, "title": n.Title, "message": n.Message, "url": n.URL, "time": n.Time.Format(time.RFC3339),
			"system": n.System, "metric": n.Metric, "value": n.Value, "threshold": n.Threshold,
		}, nil)
	case "email":
		return sendEmail(ctx, c, n)
	}
	return fmt.Errorf("unknown channel type %q", typ)
}

func plainText(n notification) string {
	s := n.Title + "\n" + n.Message
	if n.URL != "" {
		s += "\n" + n.URL
	}
	return s
}

func sendNtfy(ctx context.Context, c notifierConfig, n notification) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(n.Message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", n.Title)
	if n.Firing {
		req.Header.Set("Priority", "high")
		req.Header.Set("Tags", "warning")
	} else {
		req.Header.Set("Tags", "white_check_mark")
	}
	if n.URL != "" {
		req.Header.Set("Click", n.URL)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return do(req)
}

func postJSON(ctx context.Context, endpoint string, body any, header http.Header) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	return do(req)
}

func do(req *http.Request) error {
	resp, err := notifyClient.Do(req)
	if err != nil {
		// Don't echo URLs, which may contain tokens (Telegram, webhooks).
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return uerr.Err
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("server answered %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	return nil
}

func sendEmail(ctx context.Context, c notifierConfig, n notification) error {
	port := c.Port
	if port == 0 {
		port = 587
		if c.TLS == "tls" {
			port = 465
		}
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	tlsConfig := &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if c.TLS == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(deadline)
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if c.TLS == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS; choose TLS or none")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return err
		}
	}
	if err := client.Mail(c.From); err != nil {
		return err
	}
	var to []string
	for _, rcpt := range strings.Split(c.To, ",") {
		if rcpt = strings.TrimSpace(rcpt); rcpt != "" {
			if err := client.Rcpt(rcpt); err != nil {
				return err
			}
			to = append(to, rcpt)
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	body := n.Message
	if n.URL != "" {
		body += "\r\n\r\n" + n.URL
	}
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		c.From, strings.Join(to, ", "), mime.QEncoding.Encode("utf-8", n.Title), n.Time.Format(time.RFC1123Z),
		strings.ReplaceAll(body, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
