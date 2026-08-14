// adapter_buffer.go is the shared per-channel message store used by every adapter (Phase 11).
// Brokers deliver messages asynchronously (a reader goroutine, a subscription callback) while the
// runner consumes them synchronously (a blocking Receive with a timeout) — this buffer is the queue
// between those two sides. In-memory, WebSocket, and MQTT adapters all delegate their queueing,
// correlation matching, and wait-until-deadline behavior here instead of each reimplementing it.
package executor

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/evaluator"
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
func (b *messageBuffer) receive(channel string, corr Correlation, timeout time.Duration) (*Message, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second // default an invalid time to 30s
	}
	deadline := time.Now().Add(timeout)
	for {
		if msg := b.tryConsume(channel, corr); msg != nil {
			return msg, nil
		}
		if !time.Now().Before(deadline) {
			return nil, ErrReceiveTimeout
		}
		time.Sleep(b.pollEvery)
	}
}

// tryConsume removes and returns the first matching message, or nil if none is currently queued.
func (b *messageBuffer) tryConsume(channel string, corr Correlation) *Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.channels[channel]
	for i, m := range queue {
		if messageMatchesCorrelation(m, corr) {
			// remove index i, preserving order
			b.channels[channel] = append(queue[:i:i], queue[i+1:]...)
			return m
		}
	}
	return nil
}

// messageMatchesCorrelation reports whether a message satisfies a correlation. An empty id matches
// anything (FIFO).
//
// When the AsyncAPI document declares WHERE the id lives, that declaration decides the match on its
// own: the id is read from exactly those locations and a message that doesn't carry it there simply
// does not match. There is deliberately NO fallback to the scan below — falling back would reintroduce
// the very false positive the declaration exists to prevent (an id of "42" matching a body that
// happens to read "see ticket 42").
//
// Only when the document declares nothing does the historical heuristic apply: Metadata, then any
// header value, then any scalar anywhere in the payload, and for bytes-only messages (real brokers
// deliver Raw with no decoded Payload) a substring search of the raw body. That is imprecise by
// nature, which is why the executor warns when it is what a receive had to rely on.
func messageMatchesCorrelation(m *Message, corr Correlation) bool {
	if corr.ID == "" {
		return true
	}

	if len(corr.Locations) > 0 {
		for _, location := range corr.Locations {
			if v, ok := correlationValueAt(m, location, corr.Decode); ok && v == corr.ID {
				return true
			}
		}
		return false
	}

	if m.Metadata != nil {
		if v, ok := m.Metadata["correlationId"]; ok && fmt.Sprintf("%v", v) == corr.ID {
			return true
		}
	}
	for _, v := range m.Headers {
		if fmt.Sprintf("%v", v) == corr.ID {
			return true
		}
	}
	if m.Payload != nil {
		return payloadContainsValue(m.Payload, corr.ID)
	}
	return bytes.Contains(m.Raw, []byte(corr.ID))
}

// correlationValueAt reads the correlation id out of the one place the AsyncAPI document says it
// lives. ok=false means "this message does not carry an id there" — a malformed location, a part the
// message doesn't have, undecodable bytes, or a pointer that resolves to nothing all give the same
// answer, because for matching purposes they are the same answer.
func correlationValueAt(m *Message, location string, decode func([]byte) (interface{}, error)) (string, bool) {
	part, pointer, ok := parseCorrelationLocation(location)
	if !ok {
		return "", false
	}

	var root interface{}
	switch part {
	case "header":
		if len(m.Headers) == 0 {
			return "", false
		}
		root = m.Headers
	case "payload":
		root = m.Payload
		// A real broker delivers bytes only, so the payload must be decoded before a pointer into it
		// means anything. Decoded locally rather than cached onto the message: the executor resolves
		// the content type its own way (transport first) and must stay free to do so.
		if root == nil && len(m.Raw) > 0 && decode != nil {
			decoded, err := decode(m.Raw)
			if err != nil {
				return "", false
			}
			root = decoded
		}
		if root == nil {
			return "", false
		}
	default:
		return "", false
	}

	value := evaluator.ResolveJSONPointer(root, pointer)
	if value == nil {
		return "", false
	}
	return fmt.Sprintf("%v", value), true
}

// parseCorrelationLocation splits an AsyncAPI Correlation ID Object `location` into the part of the
// message it names and the JSON Pointer into that part. The spec requires a runtime expression of the
// form `$message.header#/…` or `$message.payload#/…`:
//
//	"$message.header#/correlationId" -> ("header",  "/correlationId")
//	"$message.payload#/user/id"      -> ("payload", "/user/id")
//
// ok=false for anything else, so an unsupported or mistyped location is a clean miss rather than a
// silent match against the wrong part of the message.
func parseCorrelationLocation(location string) (part, pointer string, ok bool) {
	location = strings.TrimSpace(location)
	root, pointer, found := strings.Cut(location, "#")
	if !found {
		return "", "", false
	}
	switch strings.TrimSpace(root) {
	case "$message.header":
		return "header", pointer, true
	case "$message.payload":
		return "payload", pointer, true
	default:
		return "", "", false
	}
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
