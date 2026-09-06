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

// Correlation describes how a receive picks its message out of everything on a channel.
//
// ID is the value to match; an empty ID matches the next message (FIFO). Locations are the places the
// AsyncAPI document says the id lives (`$message.header#/…` / `$message.payload#/…`). When Locations
// is non-empty it is AUTHORITATIVE: the id is read from exactly those places and nowhere else, because
// that declaration is the whole point — a whole-message scan would reintroduce the false positive it
// exists to prevent. When Locations is empty the matcher falls back to scanning the entire message.
type Correlation struct {
	ID        string
	Locations []string

	// Decode turns a bytes-only message into a payload so a `$message.payload#/…` location can be
	// read at all — a real broker delivers Raw with no decoded Payload. Supplied by the executor from
	// the format the AsyncAPI document declares for the channel. Nil means payload locations cannot
	// be read (header locations still work), which is a miss, never a fallback to scanning.
	Decode func([]byte) (interface{}, error)
}

// Adapter is the transport contract every broker plug-in satisfies.
type Adapter interface {
	// Name identifies the adapter for logs and errors (e.g. "in-memory").
	Name() string
	// Send publishes msg on the given channel (broker topic/queue/address).
	Send(channel string, msg *Message) error
	// Subscribe starts capturing messages on channel into the adapter's buffer, so they are held
	// until some Receive consumes them. Called once per channel before the workflow's first step, so
	// a message published between the run starting and the receive step being reached is not lost —
	// a broker does not replay what arrived while nobody was listening.
	//
	// What it means depends on the transport: MQTT subscribes to the topic, WebSocket dials the
	// channel's connection and starts its reader, and the in-memory adapter has nothing to do (its
	// queues exist from the start). Calling it more than once for a channel is harmless.
	Subscribe(channel string) error
	// Receive waits up to timeout for a message on channel matching corr; with an empty
	// corr.ID it returns the next available message (FIFO).
	// It returns ErrReceiveTimeout if none arrives in time.
	Receive(channel string, corr Correlation, timeout time.Duration) (*Message, error)
}
