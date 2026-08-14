package executor

import (
	"strings"
	"testing"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// mqttSourceDescs declares an AsyncAPI source whose `servers` selects the MQTT adapter, so
// adapterFor routes to it (the fake client is injected by seeding the adapter cache).
func mqttSourceDescs() map[string]interface{} {
	return map[string]interface{}{
		"bus": map[string]interface{}{
			"asyncapi": "3.0.0",
			"servers": map[string]interface{}{
				"broker": map[string]interface{}{"protocol": "mqtt", "host": "broker.example.com"},
			},
			"channels": map[string]interface{}{
				"orders":  map[string]interface{}{"address": "orders/new"},
				"replies": map[string]interface{}{"address": "orders/replies"},
				"audit":   map[string]interface{}{"address": "orders/audit"},
			},
			"operations": map[string]interface{}{
				"onOrder": map[string]interface{}{
					"action":  "receive",
					"channel": map[string]interface{}{"$ref": "#/channels/orders"},
				},
			},
		},
	}
}

// withFakeMQTT builds an executor whose MQTT adapter is backed by the fake broker, by seeding the
// per-broker adapter cache adapterFor consults.
func withFakeMQTT(t *testing.T) (*StepExecutor, *MQTTAdapter, *fakeMQTTClient) {
	t.Helper()
	se := NewStepExecutor(map[string]interface{}{}, mqttSourceDescs(), &models.RuntimeParams{}, &telemetry.NoopSink{})
	adapter, fake := newFakeMQTTAdapter()
	se.asyncAdapters = map[string]Adapter{"mqtt://broker.example.com": adapter}
	return se, adapter, fake
}

func receiveStep(stepID, channel string) map[string]interface{} {
	return map[string]interface{}{
		"stepId":      stepID,
		"channelPath": "bus#/channels/" + channel,
		"action":      "receive",
	}
}

// This is the whole point of pre-subscription. A peer publishes to a channel BEFORE the step that
// reads it is reached. MQTT does not replay to a late subscriber, so without warming the channel the
// message is gone; with it, the message is already in the buffer and the receive finds it.
func TestPrewarmCapturesMessagesPublishedBeforeTheReceiveStep(t *testing.T) {
	steps := []interface{}{receiveStep("await", "orders")}

	// WITHOUT prewarm: nobody is subscribed when the peer publishes, so the broker drops it.
	se, adapter, fake := withFakeMQTT(t)
	fake.Publish("orders/new", 1, false, []byte(`{"orderId":"early"}`))
	if _, err := adapter.Receive("orders/new", Correlation{}, 80*time.Millisecond); err != ErrReceiveTimeout {
		t.Fatalf("a message published with no subscriber must be lost, got %v", err)
	}

	// WITH prewarm: the subscription is in place before the peer publishes.
	se, adapter, fake = withFakeMQTT(t)
	se.PrewarmAsyncChannels(steps)
	fake.Publish("orders/new", 1, false, []byte(`{"orderId":"early"}`))

	msg, err := adapter.Receive("orders/new", Correlation{}, time.Second)
	if err != nil {
		t.Fatalf("prewarm should have captured the early message, got %v", err)
	}
	if !strings.Contains(string(msg.Raw), "early") {
		t.Errorf("got %s, want the early message", msg.Raw)
	}
}

// Only channels something RECEIVES on are warmed: subscribing fills a buffer, and a send-only channel
// has nothing that would ever drain it.
func TestPrewarmSkipsSendOnlyChannels(t *testing.T) {
	se, _, fake := withFakeMQTT(t)
	se.PrewarmAsyncChannels([]interface{}{
		map[string]interface{}{"stepId": "emit", "channelPath": "bus#/channels/audit", "action": "send"},
		receiveStep("await", "orders"),
	})

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, subscribed := fake.subs["orders/new"]; !subscribed {
		t.Error("a channel a step receives on should be warmed")
	}
	if _, subscribed := fake.subs["orders/audit"]; subscribed {
		t.Error("a send-only channel must not be warmed — nothing would ever drain its buffer")
	}
}

// The direction can come from the targeted operation rather than the step, and every targeting form
// has to resolve to the same channel — otherwise a receive written the spec-preferred way is not warmed.
func TestPrewarmResolvesEveryTargetingForm(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"channelPath":   {"stepId": "a", "channelPath": "bus#/channels/orders", "action": "receive"},
		"operationId":   {"stepId": "b", "operationId": "onOrder"},
		"operationPath": {"stepId": "c", "operationPath": "bus#/operations/onOrder"},
	}
	for form, step := range cases {
		se, _, fake := withFakeMQTT(t)
		se.PrewarmAsyncChannels([]interface{}{step})

		fake.mu.Lock()
		_, subscribed := fake.subs["orders/new"]
		fake.mu.Unlock()
		if !subscribed {
			t.Errorf("%s: the channel should have been warmed", form)
		}
	}
}

// Several steps reading one channel must produce one subscription, not one per step.
func TestPrewarmSubscribesEachChannelOnce(t *testing.T) {
	se, _, fake := withFakeMQTT(t)
	se.PrewarmAsyncChannels([]interface{}{
		receiveStep("a", "orders"),
		receiveStep("b", "orders"),
		receiveStep("c", "replies"),
	})

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.subscribeCalls != 2 {
		t.Errorf("two distinct channels should mean two subscriptions, got %d", fake.subscribeCalls)
	}
}

// A channel that cannot be reached is a WARNING, never fatal: the step that needs it will retry and
// report its own error, and a channel behind a branch the run never takes must not sink the workflow.
func TestPrewarmSurvivesASubscribeFailure(t *testing.T) {
	se := NewStepExecutor(map[string]interface{}{}, mqttSourceDescs(), &models.RuntimeParams{}, &telemetry.NoopSink{})
	se.asyncAdapters = map[string]Adapter{"mqtt://broker.example.com": &failingAdapter{}}

	// Must return normally rather than panicking or aborting.
	se.PrewarmAsyncChannels([]interface{}{receiveStep("await", "orders")})
}

// failingAdapter refuses every subscription, standing in for an unreachable broker.
type failingAdapter struct{}

func (a *failingAdapter) Name() string                { return "failing" }
func (a *failingAdapter) Send(string, *Message) error { return errFailingAdapter }
func (a *failingAdapter) Subscribe(string) error      { return errFailingAdapter }
func (a *failingAdapter) Receive(string, Correlation, time.Duration) (*Message, error) {
	return nil, errFailingAdapter
}

var errFailingAdapter = &adapterError{"connection refused"}

type adapterError struct{ msg string }

func (e *adapterError) Error() string { return e.msg }
