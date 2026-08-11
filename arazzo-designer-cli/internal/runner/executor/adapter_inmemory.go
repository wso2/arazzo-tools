// adapter_inmemory.go is a broker-less Adapter for tests and local runs: Send appends to an
// in-process per-channel FIFO queue and Receive consumes from it. It lets AsyncAPI send/receive
// workflows run end-to-end with zero external infrastructure. Real brokers arrive in Phase 11.
package executor

import (
	"fmt"
	"sync"
	"time"
)

// InMemoryAdapter implements Adapter using in-process queues.
type InMemoryAdapter struct {
	mu        sync.Mutex
	channels  map[string][]*Message // channel -> FIFO queue of un-consumed messages
	pollEvery time.Duration
}

// NewInMemoryAdapter creates an empty in-memory adapter.(constructor)
func NewInMemoryAdapter() *InMemoryAdapter {
	return &InMemoryAdapter{
		channels:  make(map[string][]*Message),
		pollEvery: 10 * time.Millisecond,
	}
}

// Name identifies this adapter.
func (a *InMemoryAdapter) Name() string { return "in-memory" }

// Send appends a message to the channel's queue.
func (a *InMemoryAdapter) Send(channel string, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("in-memory adapter: refusing to send a nil message")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.channels[channel] = append(a.channels[channel], msg)
	return nil
}

// Receive returns (and consumes) the first matching un-consumed message on the channel, polling until
// one is available or the timeout elapses. An empty correlationID matches the next message (FIFO).
func (a *InMemoryAdapter) Receive(channel, correlationID string, timeout time.Duration) (*Message, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second //default an invalid time to 30s
	}
	deadline := time.Now().Add(timeout)
	for {
		if msg := a.tryConsume(channel, correlationID); msg != nil {
			return msg, nil
		}
		if !time.Now().Before(deadline) {
			return nil, ErrReceiveTimeout
		}
		time.Sleep(a.pollEvery)
	}
}

// tryConsume removes and returns the first matching message, or nil if none is currently queued.
func (a *InMemoryAdapter) tryConsume(channel, correlationID string) *Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	queue := a.channels[channel]
	for i, m := range queue {
		if messageMatchesCorrelation(m, correlationID) {
			// remove index i, preserving order
			a.channels[channel] = append(queue[:i:i], queue[i+1:]...)
			return m
		}
	}
	return nil
}

// messageMatchesCorrelation reports whether a message satisfies a correlation id. An empty id matches
// anything (FIFO). Otherwise it matches when the id equals the message's Metadata["correlationId"], a
// header value, or a scalar anywhere in the payload. (This is a deliberately simple test-adapter
// heuristic; real brokers correlate on explicit message headers — Phase 11.)
func messageMatchesCorrelation(m *Message, correlationID string) bool {
	if correlationID == "" {
		return true
	}
	if m.Metadata != nil {
		if v, ok := m.Metadata["correlationId"]; ok && fmt.Sprintf("%v", v) == correlationID {
			return true
		}
	}
	for _, v := range m.Headers {
		if fmt.Sprintf("%v", v) == correlationID {
			return true
		}
	}
	return payloadContainsValue(m.Payload, correlationID)
}

// payloadContainsValue reports whether target appears as a scalar value anywhere in v (recursing
// through maps and slices).
func payloadContainsValue(v interface{}, target string) bool {
	switch x := v.(type) {
	case map[string]interface{}:
		for _, val := range x {
			if payloadContainsValue(val, target) {
				return true
			}
		}
	case []interface{}:
		for _, val := range x {
			if payloadContainsValue(val, target) {
				return true
			}
		}
	case nil:
		return false
	default:
		return fmt.Sprintf("%v", x) == target
	}
	return false
}
