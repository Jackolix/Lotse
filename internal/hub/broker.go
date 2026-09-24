package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Jackolix/Lotse/internal/hub/store"
)

// broker fans out server-sent events to every open UI. Its subscriber count also
// decides whether agents report in live mode.
type broker struct {
	mu       sync.Mutex
	subs     map[chan []byte]struct{}
	onChange func()
}

func newBroker(onChange func()) *broker {
	return &broker{subs: map[chan []byte]struct{}{}, onChange: onChange}
}

func (b *broker) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

func (b *broker) subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	b.onChange()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		b.onChange()
	}
}

// publish sends an event to every subscriber. A viewer that falls behind misses
// events instead of slowing down agents.
func (b *broker) publish(event string, data any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) == 0 {
		return
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := []byte("event: " + event + "\ndata: " + string(payload) + "\n\n")
	for ch := range b.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// handleEvents streams events to the browser: "metrics" for every sample, "status"
// when an agent connects or disconnects, "systems" when the list changed.
func (h *Hub) handleEvents(w http.ResponseWriter, r *http.Request, _ *store.User) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream

	ch, unsubscribe := h.broker.subscribe()
	defer unsubscribe()

	if _, err := io.WriteString(w, ": connected\n\n"); err != nil {
		return
	}
	if err := rc.Flush(); err != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}
