package protocol

import (
	"encoding/json"
	"errors"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrTimeout is returned when the peer does not answer a request in time.
// The connection is closed in that case, since it is most likely dead.
var ErrTimeout = errors.New("request timed out")

// Send marshals v as JSON and sends it as an SSH global request.
// x/crypto/ssh has no per-request deadline, so a stuck peer is handled by closing the connection.
func Send(conn ssh.Conn, name string, wantReply bool, v any, timeout time.Duration) (ok bool, reply []byte, err error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return false, nil, err
	}
	type result struct {
		ok    bool
		reply []byte
		err   error
	}
	done := make(chan result, 1)
	go func() {
		ok, reply, err := conn.SendRequest(name, wantReply, payload)
		done <- result{ok, reply, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.ok, r.reply, r.err
	case <-timer.C:
		conn.Close()
		return false, nil, ErrTimeout
	}
}

// Reply answers a request with a JSON payload. It is a no-op if no reply was requested.
func Reply(req *ssh.Request, ok bool, v any) error {
	if !req.WantReply {
		return nil
	}
	var payload []byte
	if v != nil {
		var err error
		if payload, err = json.Marshal(v); err != nil {
			return err
		}
	}
	return req.Reply(ok, payload)
}

// RejectChannels refuses every incoming channel. Agents never open channels to the hub.
func RejectChannels(chans <-chan ssh.NewChannel) {
	for ch := range chans {
		ch.Reject(ssh.Prohibited, "not supported")
	}
}
