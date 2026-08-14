// adapter_inmemory.go is a broker-less Adapter for tests and local runs: Send appends to an
// in-process per-channel FIFO queue and Receive consumes from it. It lets AsyncAPI send/receive
// workflows run end-to-end with zero external infrastructure. Real brokers (WebSocket, MQTT) live
// in adapter_ws.go / adapter_mqtt.go; the queueing itself is the shared messageBuffer.
package executor

import (
	"fmt"
	"time"
)

// InMemoryAdapter implements Adapter using in-process queues.
type InMemoryAdapter struct {
	buffer *messageBuffer
}

// NewInMemoryAdapter creates an empty in-memory adapter.(constructor)
func NewInMemoryAdapter() *InMemoryAdapter {
	return &InMemoryAdapter{buffer: newMessageBuffer()}
}

// Name identifies this adapter.
func (a *InMemoryAdapter) Name() string { return "in-memory" }

// Send appends a message to the channel's queue.
func (a *InMemoryAdapter) Send(channel string, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("in-memory adapter: refusing to send a nil message")
	}
	a.buffer.push(channel, msg)
	return nil
}

// Subscribe is a no-op: there is no broker to register with, and the channel's queue already exists
// (and is written to) from the moment anything sends on it. Nothing can be missed here, so nothing
// needs warming up.
func (a *InMemoryAdapter) Subscribe(string) error { return nil }

// Receive returns (and consumes) the first matching un-consumed message on the channel, polling until
// one is available or the timeout elapses. An empty corr.ID matches the next message (FIFO).
func (a *InMemoryAdapter) Receive(channel string, corr Correlation, timeout time.Duration) (*Message, error) {
	return a.buffer.receive(channel, corr, timeout)
}
