// adapter_buffer.go is the shared per-channel message store used by every adapter (Phase 11).
// Brokers deliver messages asynchronously (a reader goroutine, a subscription callback) while the
// runner consumes them synchronously (a blocking Receive with a timeout) — this buffer is the queue
// between those two sides. In-memory, WebSocket, and MQTT adapters all delegate their queueing,
// correlation matching, and wait-until-deadline behavior here instead of each reimplementing it.
package executor

import (
	"bytes"
	"fmt"
	"sync"
	"time"
)

// messageBuffer holds un-consumed messages per channel and hands them out FIFO (or by correlation).
type messageBuffer struct {
	mu        sync.Mutex
	channels  map[string][]*Message // channel -> FIFO queue of un-consumed messages
	pollEvery time.Duration
}

// newMessageBuffer creates an empty buffer.
func newMessageBuffer() *messageBuffer {
	return &messageBuffer{
		channels:  make(map[string][]*Message),
		pollEvery: 10 * time.Millisecond,
	}
}

// push appends a message to the channel's queue (called by adapter readers/callbacks).
func (b *messageBuffer) push(channel string, msg *Message) {
	if msg == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channels[channel] = append(b.channels[channel], msg)
}

// receive returns (and consumes) the first matching un-consumed message on the channel, polling
// until one is available or the timeout elapses. An empty correlationID matches the next message
// (FIFO). It returns ErrReceiveTimeout when nothing matching arrives in time.
func (b *messageBuffer) receive(channel, correlationID string, timeout time.Duration) (*Message, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second // default an invalid time to 30s
	}
	deadline := time.Now().Add(timeout)
	for {
		if msg := b.tryConsume(channel, correlationID); msg != nil {
			return msg, nil
		}
		if !time.Now().Before(deadline) {
			return nil, ErrReceiveTimeout
		}
		time.Sleep(b.pollEvery)
	}
}

// tryConsume removes and returns the first matching message, or nil if none is currently queued.
func (b *messageBuffer) tryConsume(channel, correlationID string) *Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.channels[channel]
	for i, m := range queue {
		if messageMatchesCorrelation(m, correlationID) {
			// remove index i, preserving order
			b.channels[channel] = append(queue[:i:i], queue[i+1:]...)
			return m
		}
	}
	return nil
}

// messageMatchesCorrelation reports whether a message satisfies a correlation id. An empty id matches
// anything (FIFO). Otherwise it matches when the id equals the message's Metadata["correlationId"], a
// header value, or a scalar anywhere in the payload; for messages that arrived as bytes only (real
// brokers deliver Raw with no decoded Payload) it falls back to a substring search of the raw body.
// (This is a deliberately simple heuristic; schema-declared correlation locations are a later step.)
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
	if m.Payload != nil {
		return payloadContainsValue(m.Payload, correlationID)
	}
	return bytes.Contains(m.Raw, []byte(correlationID))
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
