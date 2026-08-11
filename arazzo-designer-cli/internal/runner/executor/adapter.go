// adapter.go defines the broker-agnostic transport boundary for AsyncAPI steps (Phase 9). The runner
// never speaks a specific broker's wire protocol; it calls Send/Receive on an Adapter, and a concrete
// adapter implements that for a given broker. Phase 9 ships only InMemoryAdapter (a broker-less test
// adapter); real brokers (Kafka/MQTT/WebSocket/...) are Phase 11. Serialization (object <-> bytes) is
// a separate concern handled by the caller and formalized in Phase 10.
package executor

import (
	"errors"
	"time"
)

// Message is a normalized message exchanged with an adapter. Payload is the decoded body used by
// runtime expressions ($message.payload); Headers back $message.header.*. Raw/ContentType carry the
// serialized form (populated best-effort in Phase 9), and Metadata holds adapter details (e.g. topic).
type Message struct {
	Payload     interface{}
	Headers     map[string]interface{}
	ContentType string
	Raw         []byte
	Metadata    map[string]interface{}
}

// ErrReceiveTimeout is returned by Receive when no matching message arrives within the timeout.
var ErrReceiveTimeout = errors.New("receive timed out waiting for a message")

// Adapter is the transport contract every broker plug-in satisfies.
type Adapter interface {
	// Name identifies the adapter for logs and errors (e.g. "in-memory").
	Name() string
	// Send publishes msg on the given channel (broker topic/queue/address).
	Send(channel string, msg *Message) error
	// Receive waits up to timeout for a message on channel. When correlationID is non-empty it
	// returns the first message correlated to that value; otherwise the next available one (FIFO).
	// It returns ErrReceiveTimeout if none arrives in time.
	Receive(channel, correlationID string, timeout time.Duration) (*Message, error)
}
