// adapter_mqtt.go is the MQTT Adapter (Phase 11). The AsyncAPI channel address is the MQTT topic:
// Send publishes to it and Receive consumes from a subscription that feeds the shared messageBuffer.
// The adapter subscribes to a topic BEFORE publishing on it, so a send→receive round trip within one
// workflow works against a real broker (the broker echoes our own publication back to our
// subscription; without the early subscribe the message would be gone before the receive step runs).
// The paho client sits behind the small mqttClient interface so unit tests can substitute a fake.
package executor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// mqttOpTimeout bounds how long connect/subscribe/publish may block.
const mqttOpTimeout = 10 * time.Second

// mqttClient is the slice of paho's mqtt.Client the adapter actually uses (fakeable in tests).
type mqttClient interface {
	Connect() mqtt.Token
	Publish(topic string, qos byte, retained bool, payload interface{}) mqtt.Token
	Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token
	IsConnected() bool
}

// MQTTAdapter implements Adapter against one MQTT broker.
type MQTTAdapter struct {
	brokerURL string // paho URL, e.g. "tcp://broker:1883" or "ssl://broker:8883"
	buffer    *messageBuffer

	mu         sync.Mutex
	client     mqttClient
	subscribed map[string]bool // topics with a live subscription

	// newClient builds the underlying client on first use; tests replace it with a fake factory.
	newClient func(brokerURL string) mqttClient
}

// NewMQTTAdapter creates an MQTT adapter for one broker. protocol is "mqtt" (tcp, default port 1883)
// or "mqtts" (TLS, default port 8883); host is "host" or "host:port" from the AsyncAPI server.
func NewMQTTAdapter(protocol, host string) *MQTTAdapter {
	scheme, defaultPort := "tcp", "1883"
	if strings.EqualFold(protocol, "mqtts") || strings.EqualFold(protocol, "secure-mqtt") {
		scheme, defaultPort = "ssl", "8883"
	}
	host = strings.TrimSpace(host)
	if !strings.Contains(host, ":") {
		host += ":" + defaultPort
	}
	return &MQTTAdapter{
		brokerURL:  scheme + "://" + host,
		buffer:     newMessageBuffer(),
		subscribed: map[string]bool{},
		newClient:  newPahoClient,
	}
}

// newPahoClient is the production mqttClient factory.
func newPahoClient(brokerURL string) mqttClient {
	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(fmt.Sprintf("arazzo-runner-%d", time.Now().UnixNano())).
		SetCleanSession(true).
		SetConnectTimeout(mqttOpTimeout)
	return mqtt.NewClient(opts)
}

// Name identifies this adapter.
func (a *MQTTAdapter) Name() string { return "mqtt" }

// Send publishes the message's raw bytes on the topic (QoS 1). It subscribes to the topic first so a
// later Receive on the same channel can consume the broker's echo of this publication.
func (a *MQTTAdapter) Send(channel string, msg *Message) error {
	if msg == nil {
		return fmt.Errorf("mqtt adapter: refusing to send a nil message")
	}
	if err := a.ensureSubscribed(channel); err != nil {
		return err
	}
	client := a.currentClient()
	if err := waitToken(client.Publish(channel, 1, false, msg.Raw), "publish to "+channel); err != nil {
		return err
	}
	return nil
}

// Receive waits for a message on the topic; the subscription callback feeds the shared buffer and
// this consumes the first match (FIFO or by correlation id).
func (a *MQTTAdapter) Receive(channel, correlationID string, timeout time.Duration) (*Message, error) {
	if err := a.ensureSubscribed(channel); err != nil {
		return nil, err
	}
	return a.buffer.receive(channel, correlationID, timeout)
}

// ensureSubscribed connects on first use and subscribes to the topic once; incoming publications are
// pushed into the buffer keyed by the topic they were requested on.
func (a *MQTTAdapter) ensureSubscribed(channel string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		a.client = a.newClient(a.brokerURL)
	}
	if !a.client.IsConnected() {
		if err := waitToken(a.client.Connect(), "connect to "+a.brokerURL); err != nil {
			return err
		}
	}
	if a.subscribed[channel] {
		return nil
	}
	handler := func(_ mqtt.Client, m mqtt.Message) {
		a.buffer.push(channel, &Message{
			Raw:      m.Payload(),
			Metadata: map[string]interface{}{"channel": m.Topic(), "transport": "mqtt"},
		})
	}
	if err := waitToken(a.client.Subscribe(channel, 1, handler), "subscribe to "+channel); err != nil {
		return err
	}
	a.subscribed[channel] = true
	return nil
}

// currentClient returns the connected client (only valid after ensureSubscribed succeeded).
func (a *MQTTAdapter) currentClient() mqttClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

// waitToken waits for an MQTT operation to complete and normalizes its failure/timeout into an error.
func waitToken(t mqtt.Token, op string) error {
	if !t.WaitTimeout(mqttOpTimeout) {
		return fmt.Errorf("mqtt %s timed out after %s", op, mqttOpTimeout)
	}
	if err := t.Error(); err != nil {
		return fmt.Errorf("mqtt %s failed: %w", op, err)
	}
	return nil
}
