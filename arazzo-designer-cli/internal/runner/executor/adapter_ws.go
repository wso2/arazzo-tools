// adapter_ws.go is the WebSocket Adapter (Phase 11) — the first real network transport. A WebSocket
// is a bidirectional pipe to a server, so "channel" maps to the URL path: the adapter keeps one
// connection per channel (scheme://host/<channel-address>), a reader goroutine drains everything the
// server sends into the shared messageBuffer, Send writes frames, and Receive consumes from the
// buffer with the usual timeout + correlation semantics. wss:// gives TLS (handled by the dialer).
package executor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsDialTimeout / wsWriteTimeout bound how long connecting and writing may block.
const (
	wsDialTimeout  = 10 * time.Second
	wsWriteTimeout = 10 * time.Second
)

// WSAdapter implements Adapter over WebSocket connections to a single server.
type WSAdapter struct {
	baseURL string // "ws://host" or "wss://host" (no trailing slash)
	buffer  *messageBuffer

	mu    sync.Mutex
	conns map[string]*wsConn // channel -> live connection
}

// wsConn pairs a connection with a write lock (gorilla allows only one concurrent writer).
type wsConn struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

// NewWSAdapter creates a WebSocket adapter for one server. host may be "ws://...", "wss://...", or a
// bare "host[:port]" (defaulting to ws://).
func NewWSAdapter(host string) *WSAdapter {
	base := strings.TrimRight(strings.TrimSpace(host), "/")
	if !strings.HasPrefix(base, "ws://") && !strings.HasPrefix(base, "wss://") {
		base = "ws://" + base
	}
	return &WSAdapter{
		baseURL: base,
		buffer:  newMessageBuffer(),
		conns:   map[string]*wsConn{},
	}
}

// Name identifies this adapter.
func (a *WSAdapter) Name() string { return "websocket" }

// Send writes the message's raw bytes as one WebSocket text frame on the channel's connection.
func (a *WSAdapter) Send(channel string, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("websocket adapter: refusing to send a nil message")
	}
	wc, err := a.ensureConn(channel)
	if err != nil {
		return err
	}
	wc.writeMu.Lock()
	defer wc.writeMu.Unlock()
	_ = wc.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if err := wc.conn.WriteMessage(websocket.TextMessage, msg.Raw); err != nil {
		a.dropConn(channel, wc) // stale connection; next use redials
		return fmt.Errorf("websocket write to %s failed: %w", a.channelURL(channel), err)
	}
	return nil
}

// Subscribe dials the channel's connection and starts its reader goroutine early. A WebSocket has no
// subscribe step — the connection IS the subscription, and the server can push a frame the moment it
// is open — so connecting up front is exactly what stops an early frame being missed. It also means a
// server that greets on connect is greeted at workflow start rather than at the receive step.
func (a *WSAdapter) Subscribe(channel string) error {
	_, err := a.ensureConn(channel)
	return err
}

// Receive waits for a message on the channel. The connection's reader goroutine feeds everything the
// server pushes into the buffer; this consumes the first match (FIFO or by correlation id).
func (a *WSAdapter) Receive(channel string, corr Correlation, timeout time.Duration) (*Message, error) {
	if _, err := a.ensureConn(channel); err != nil {
		return nil, err
	}
	return a.buffer.receive(channel, corr, timeout)
}

// ensureConn returns the channel's live connection, dialing (and starting its reader) if needed.
func (a *WSAdapter) ensureConn(channel string) (*wsConn, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if wc, ok := a.conns[channel]; ok {
		return wc, nil
	}
	url := a.channelURL(channel)
	dialer := websocket.Dialer{HandshakeTimeout: wsDialTimeout}
	conn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("websocket connect to %s failed: %w", url, err)
	}
	wc := &wsConn{conn: conn}
	a.conns[channel] = wc
	go a.readLoop(channel, wc)
	return wc, nil
}

// readLoop drains incoming frames into the buffer until the connection dies, then drops it so the
// next Send/Receive redials.
func (a *WSAdapter) readLoop(channel string, wc *wsConn) {
	for {
		_, data, err := wc.conn.ReadMessage()
		if err != nil {
			a.dropConn(channel, wc)
			return
		}
		a.buffer.push(channel, &Message{
			Raw:      data,
			Metadata: map[string]interface{}{"channel": channel, "transport": "websocket"},
		})
	}
}

// dropConn closes and forgets a channel's connection (safe to call twice).
//
// It only forgets the connection that actually failed. A reader goroutine outlives its connection by
// the moment it takes to unwind, so Send and readLoop can both report the SAME dead connection — and
// if a later Send/Receive has already redialled in between, dropping blindly would close the live
// replacement and leave the channel silently disconnected.
func (a *WSAdapter) dropConn(channel string, failed *wsConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if wc, ok := a.conns[channel]; ok && wc == failed {
		_ = wc.conn.Close()
		delete(a.conns, channel)
	}
}

// channelURL joins the server base URL and the channel address as the request path.
func (a *WSAdapter) channelURL(channel string) string {
	path := strings.TrimLeft(strings.TrimSpace(channel), "/")
	if path == "" {
		return a.baseURL + "/"
	}
	return a.baseURL + "/" + path
}
