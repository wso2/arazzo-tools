package executor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// ---- shared buffer: raw-bytes correlation (real brokers deliver Raw with no decoded Payload) ----

func TestMessageBuffer_RawBytesCorrelation(t *testing.T) {
	b := newMessageBuffer()
	b.push("ch", &Message{Raw: []byte(`{"orderId":"A"}`)})
	b.push("ch", &Message{Raw: []byte(`{"orderId":"B"}`)})

	m, err := b.receive("ch", "B", time.Second)
	if err != nil || !strings.Contains(string(m.Raw), "B") {
		t.Fatalf("raw correlation should match the B message, got %v / %v", m, err)
	}
	m2, _ := b.receive("ch", "", time.Second)
	if !strings.Contains(string(m2.Raw), "A") {
		t.Errorf("the A message should remain queued, got %s", m2.Raw)
	}
}

// ---- WebSocket adapter against a real local echo server ----

// startWSEchoServer runs a WebSocket echo server on a local port and returns its host ("127.0.0.1:port").
func startWSEchoServer(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestWSAdapter_EchoRoundTrip(t *testing.T) {
	a := NewWSAdapter(startWSEchoServer(t))

	if err := a.Send("echo", &Message{Raw: []byte(`{"ping":"pong"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	msg, err := a.Receive("echo", "", 2*time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if string(msg.Raw) != `{"ping":"pong"}` {
		t.Errorf("echoed bytes mismatch: %s", msg.Raw)
	}
}

func TestWSAdapter_ConnectFailure(t *testing.T) {
	a := NewWSAdapter("127.0.0.1:1") // nothing listens on port 1
	if err := a.Send("echo", &Message{Raw: []byte("x")}); err == nil {
		t.Error("send to an unreachable server should fail")
	}
}

// TestWSAdapter_ExecuteStepE2E drives a full send->receive workflow through ExecuteStep with an
// AsyncAPI source whose `servers` declares ws://<local echo server> — covering adapter selection,
// Phase-10 serialization on send, and raw-byte deserialization on receive against a real socket.
func TestWSAdapter_ExecuteStepE2E(t *testing.T) {
	host := startWSEchoServer(t)
	srcs := map[string]interface{}{
		"wsBus": map[string]interface{}{
			"asyncapi": "3.0.0",
			"servers": map[string]interface{}{
				"local": map[string]interface{}{"host": host, "protocol": "ws"},
			},
			"channels": map[string]interface{}{
				"echo": map[string]interface{}{"address": "echo"},
			},
		},
	}
	se := NewStepExecutor(map[string]interface{}{}, srcs, &models.RuntimeParams{}, &telemetry.NoopSink{})
	state := models.NewExecutionState("wf", nil, nil, nil)

	send := map[string]interface{}{
		"stepId": "ping", "channelPath": "wsBus#/channels/echo", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"ping": "pong"}},
	}
	if r := se.ExecuteStep(send, nil, state); !r.Success {
		t.Fatalf("ws send failed: %s", r.Error)
	}

	recv := map[string]interface{}{
		"stepId": "pong", "channelPath": "wsBus#/channels/echo", "action": "receive", "timeout": 2000,
		"successCriteria": []interface{}{map[string]interface{}{"condition": `$message.payload.ping == "pong"`}},
		"outputs":         map[string]interface{}{"got": "$message.payload.ping"},
	}
	r := se.ExecuteStep(recv, nil, state)
	if !r.Success {
		t.Fatalf("ws receive failed: %s", r.Error)
	}
	if r.Outputs["got"] != "pong" {
		t.Errorf("expected echoed payload back through $message, got %v", r.Outputs["got"])
	}
}

// ---- MQTT adapter against a fake client (a real broker is opt-in, below) ----

type fakeToken struct{ err error }

func (t *fakeToken) Wait() bool                     { return true }
func (t *fakeToken) WaitTimeout(time.Duration) bool { return true }
func (t *fakeToken) Done() <-chan struct{}          { ch := make(chan struct{}); close(ch); return ch }
func (t *fakeToken) Error() error                   { return t.err }

type fakeMQTTMessage struct {
	topic   string
	payload []byte
}

func (m *fakeMQTTMessage) Duplicate() bool   { return false }
func (m *fakeMQTTMessage) Qos() byte         { return 1 }
func (m *fakeMQTTMessage) Retained() bool    { return false }
func (m *fakeMQTTMessage) Topic() string     { return m.topic }
func (m *fakeMQTTMessage) MessageID() uint16 { return 0 }
func (m *fakeMQTTMessage) Payload() []byte   { return m.payload }
func (m *fakeMQTTMessage) Ack()              {}

// fakeMQTTClient behaves like a broker for one client: a publish is echoed back to this client's own
// subscription on the topic (which is what a real broker does for a subscribed sender).
type fakeMQTTClient struct {
	mu        sync.Mutex
	connected bool
	subs      map[string]mqtt.MessageHandler
}

func newFakeMQTTClient() *fakeMQTTClient {
	return &fakeMQTTClient{subs: map[string]mqtt.MessageHandler{}}
}

func (c *fakeMQTTClient) Connect() mqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = true
	return &fakeToken{}
}

func (c *fakeMQTTClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *fakeMQTTClient) Subscribe(topic string, _ byte, cb mqtt.MessageHandler) mqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subs[topic] = cb
	return &fakeToken{}
}

func (c *fakeMQTTClient) Publish(topic string, _ byte, _ bool, payload interface{}) mqtt.Token {
	c.mu.Lock()
	cb := c.subs[topic]
	c.mu.Unlock()
	if cb != nil {
		cb(nil, &fakeMQTTMessage{topic: topic, payload: payload.([]byte)})
	}
	return &fakeToken{}
}

// newFakeMQTTAdapter wires an MQTTAdapter to the fake client instead of a real paho connection.
func newFakeMQTTAdapter() (*MQTTAdapter, *fakeMQTTClient) {
	fake := newFakeMQTTClient()
	a := NewMQTTAdapter("mqtt", "fake-broker")
	a.newClient = func(string) mqttClient { return fake }
	return a, fake
}

func TestMQTTAdapter_SendSubscribesFirstAndRoundTrips(t *testing.T) {
	a, fake := newFakeMQTTAdapter()

	if err := a.Send("orders/new", &Message{Raw: []byte(`{"orderId":"M1"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	// Send must have subscribed BEFORE publishing (that ordering is what makes the round trip work).
	fake.mu.Lock()
	_, subscribed := fake.subs["orders/new"]
	fake.mu.Unlock()
	if !subscribed {
		t.Fatal("Send should subscribe to the topic before publishing")
	}
	msg, err := a.Receive("orders/new", "", time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if string(msg.Raw) != `{"orderId":"M1"}` {
		t.Errorf("round-tripped bytes mismatch: %s", msg.Raw)
	}
}

func TestMQTTAdapter_ReceiveTimeout(t *testing.T) {
	a, _ := newFakeMQTTAdapter()
	if _, err := a.Receive("quiet/topic", "", 60*time.Millisecond); err != ErrReceiveTimeout {
		t.Fatalf("expected ErrReceiveTimeout, got %v", err)
	}
}

func TestMQTTAdapter_BrokerURLMapping(t *testing.T) {
	if a := NewMQTTAdapter("mqtt", "broker.example.com"); a.brokerURL != "tcp://broker.example.com:1883" {
		t.Errorf("mqtt default port: got %s", a.brokerURL)
	}
	if a := NewMQTTAdapter("mqtts", "broker.example.com"); a.brokerURL != "ssl://broker.example.com:8883" {
		t.Errorf("mqtts default port: got %s", a.brokerURL)
	}
	if a := NewMQTTAdapter("mqtt", "broker.example.com:9001"); a.brokerURL != "tcp://broker.example.com:9001" {
		t.Errorf("explicit port must be kept: got %s", a.brokerURL)
	}
}

// TestMQTTAdapter_RealBroker is an opt-in integration test: set ARAZZO_TEST_MQTT_BROKER (e.g.
// "broker.hivemq.com") to run a real network round trip. Skipped otherwise so CI needs no broker.
func TestMQTTAdapter_RealBroker(t *testing.T) {
	broker := os.Getenv("ARAZZO_TEST_MQTT_BROKER")
	if broker == "" {
		t.Skip("set ARAZZO_TEST_MQTT_BROKER to run the real-broker integration test")
	}
	a := NewMQTTAdapter("mqtt", broker)
	topic := "arazzo/it/" + time.Now().Format("150405.000000000")
	if err := a.Send(topic, &Message{Raw: []byte(`{"it":"works"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	msg, err := a.Receive(topic, "", 10*time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if string(msg.Raw) != `{"it":"works"}` {
		t.Errorf("round-tripped bytes mismatch: %s", msg.Raw)
	}
}

// ---- adapter selection from AsyncAPI servers ----

func specWithServer(protocol, host string) map[string]interface{} {
	return map[string]interface{}{
		"asyncapi": "3.0.0",
		"servers": map[string]interface{}{
			"prod": map[string]interface{}{"protocol": protocol, "host": host},
		},
		"channels": map[string]interface{}{"c": map[string]interface{}{"address": "c"}},
	}
}

func selectorExecutor(spec map[string]interface{}) *StepExecutor {
	return NewStepExecutor(map[string]interface{}{}, map[string]interface{}{"bus": spec}, &models.RuntimeParams{}, &telemetry.NoopSink{})
}

func TestAdapterFor_Selection(t *testing.T) {
	// ws -> WSAdapter
	se := selectorExecutor(specWithServer("ws", "example.com"))
	a, err := se.adapterFor(&AsyncInfo{Source: "bus"})
	if err != nil {
		t.Fatalf("ws: %v", err)
	}
	if _, ok := a.(*WSAdapter); !ok {
		t.Errorf("ws should select WSAdapter, got %T", a)
	}

	// mqtt -> MQTTAdapter
	se = selectorExecutor(specWithServer("mqtt", "example.com"))
	a, _ = se.adapterFor(&AsyncInfo{Source: "bus"})
	if _, ok := a.(*MQTTAdapter); !ok {
		t.Errorf("mqtt should select MQTTAdapter, got %T", a)
	}

	// kafka -> clear not-yet-supported error
	se = selectorExecutor(specWithServer("kafka", "example.com"))
	if _, err := se.adapterFor(&AsyncInfo{Source: "bus"}); err == nil || !strings.Contains(err.Error(), "not yet supported") {
		t.Errorf("kafka should be a clear not-yet-supported error, got %v", err)
	}

	// unknown protocol -> clear error
	se = selectorExecutor(specWithServer("amqp", "example.com"))
	if _, err := se.adapterFor(&AsyncInfo{Source: "bus"}); err == nil {
		t.Error("unknown protocol should error")
	}

	// no servers -> the default (in-memory) adapter
	se = selectorExecutor(map[string]interface{}{"asyncapi": "3.0.0"})
	a, err = se.adapterFor(&AsyncInfo{Source: "bus"})
	if err != nil || a != se.AsyncAdapter {
		t.Errorf("no servers should fall back to the default adapter, got %T / %v", a, err)
	}
}

func TestAdapterFor_CachesPerBroker(t *testing.T) {
	se := selectorExecutor(specWithServer("ws", "example.com"))
	a1, _ := se.adapterFor(&AsyncInfo{Source: "bus"})
	a2, _ := se.adapterFor(&AsyncInfo{Source: "bus"})
	if a1 != a2 {
		t.Error("the same protocol://host must reuse one cached adapter (one connection per broker)")
	}
}

// ---- receive-side contentType fallback from the AsyncAPI channel declaration ----

func TestChannelMessageContentType(t *testing.T) {
	info := &AsyncInfo{Channel: map[string]interface{}{
		"messages": map[string]interface{}{
			"alert": map[string]interface{}{"contentType": "text/plain"},
		},
	}}
	if ct := channelMessageContentType(info); ct != "text/plain" {
		t.Errorf("expected declared text/plain, got %q", ct)
	}
	if ct := channelMessageContentType(&AsyncInfo{Channel: map[string]interface{}{}}); ct != "" {
		t.Errorf("no declaration should be empty, got %q", ct)
	}
	if ct := channelMessageContentType(nil); ct != "" {
		t.Errorf("nil info should be empty, got %q", ct)
	}
}
