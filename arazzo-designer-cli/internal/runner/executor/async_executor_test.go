package executor

import (
	"testing"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// asyncSourceDescs mirrors the phase8 example: one AsyncAPI source with an `orders` channel.
func asyncSourceDescs() map[string]interface{} {
	return map[string]interface{}{
		"orderBus": map[string]interface{}{
			"asyncapi": "3.0.0",
			"channels": map[string]interface{}{
				"orders": map[string]interface{}{"address": "orders/new"},
			},
			"operations": map[string]interface{}{
				"placeOrder": map[string]interface{}{
					"action":  "send",
					"channel": map[string]interface{}{"$ref": "#/channels/orders"},
				},
			},
		},
	}
}

func newAsyncExecutor() *StepExecutor {
	return NewStepExecutor(map[string]interface{}{}, asyncSourceDescs(), &models.RuntimeParams{}, &telemetry.NoopSink{})
}

// ---- adapter-level tests ----

func TestInMemoryAdapter_FIFO(t *testing.T) {
	a := NewInMemoryAdapter()
	_ = a.Send("ch", &Message{Payload: map[string]interface{}{"n": 1}})
	_ = a.Send("ch", &Message{Payload: map[string]interface{}{"n": 2}})

	m1, err := a.Receive("ch", "", time.Second)
	if err != nil || m1.Payload.(map[string]interface{})["n"] != 1 {
		t.Fatalf("first receive: %v / %v", m1, err)
	}
	m2, _ := a.Receive("ch", "", time.Second)
	if m2.Payload.(map[string]interface{})["n"] != 2 {
		t.Errorf("second receive should be the 2nd message, got %v", m2.Payload)
	}
}

func TestInMemoryAdapter_Correlation(t *testing.T) {
	a := NewInMemoryAdapter()
	_ = a.Send("ch", &Message{Payload: map[string]interface{}{"orderId": "A"}})
	_ = a.Send("ch", &Message{Payload: map[string]interface{}{"orderId": "B"}})

	// Ask for "B" first — correlation should skip "A" and return the B message.
	m, err := a.Receive("ch", "B", time.Second)
	if err != nil || m.Payload.(map[string]interface{})["orderId"] != "B" {
		t.Fatalf("correlated receive: %v / %v", m, err)
	}
	// "A" is still queued.
	m2, _ := a.Receive("ch", "A", time.Second)
	if m2 == nil || m2.Payload.(map[string]interface{})["orderId"] != "A" {
		t.Errorf("expected the A message to remain, got %v", m2)
	}
}

func TestInMemoryAdapter_Timeout(t *testing.T) {
	a := NewInMemoryAdapter()
	start := time.Now()
	_, err := a.Receive("empty", "", 60*time.Millisecond)
	if err != ErrReceiveTimeout {
		t.Fatalf("expected ErrReceiveTimeout, got %v", err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Errorf("receive returned too early to have waited for the timeout")
	}
}

// ---- action resolution ----

func TestResolveAsyncAction(t *testing.T) {
	// operation action wins
	if a, err := resolveAsyncAction(map[string]interface{}{}, &AsyncInfo{Action: "send"}); err != nil || a != "send" {
		t.Errorf("op action: got %q / %v", a, err)
	}
	// contradicting step action -> operation still wins (warn only)
	if a, _ := resolveAsyncAction(map[string]interface{}{"action": "receive"}, &AsyncInfo{Action: "send"}); a != "send" {
		t.Errorf("mismatch should prefer op action, got %q", a)
	}
	// channel-only (no op action) requires a step action
	if _, err := resolveAsyncAction(map[string]interface{}{}, &AsyncInfo{}); err == nil {
		t.Error("channel-only without action should error")
	}
	// channel-only with a valid action
	if a, err := resolveAsyncAction(map[string]interface{}{"action": "receive"}, &AsyncInfo{}); err != nil || a != "receive" {
		t.Errorf("channel-only receive: got %q / %v", a, err)
	}
	// invalid action
	if _, err := resolveAsyncAction(map[string]interface{}{"action": "publish"}, &AsyncInfo{}); err == nil {
		t.Error("invalid action should error")
	}
}

// ---- end-to-end through ExecuteStep ----

func TestAsyncSendReceiveRoundTrip(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)

	send := map[string]interface{}{
		"stepId":      "send",
		"channelPath": "orderBus#/channels/orders",
		"action":      "send",
		"requestBody": map[string]interface{}{
			"payload": map[string]interface{}{"orderId": "A1", "status": "new"},
		},
	}
	if r := se.ExecuteStep(send, nil, state); !r.Success {
		t.Fatalf("send step failed: %s", r.Error)
	}

	recv := map[string]interface{}{
		"stepId":      "recv",
		"channelPath": "orderBus#/channels/orders",
		"action":      "receive",
		"successCriteria": []interface{}{
			map[string]interface{}{"condition": `$message.payload.status == "new"`},
		},
		"outputs": map[string]interface{}{
			"id": "$message.payload.orderId",
		},
	}
	r := se.ExecuteStep(recv, nil, state)
	if !r.Success {
		t.Fatalf("receive step failed: %s", r.Error)
	}
	if r.Outputs["id"] != "A1" {
		t.Errorf("expected output id=A1 from $message.payload.orderId, got %v", r.Outputs["id"])
	}
}

func TestAsyncReceiveCriteriaFailure(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)

	se.ExecuteStep(map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"status": "pending"}},
	}, nil, state)

	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "recv", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"successCriteria": []interface{}{map[string]interface{}{"condition": `$message.payload.status == "confirmed"`}},
	}, nil, state)
	if r.Success {
		t.Error("receive should fail when the message doesn't satisfy successCriteria")
	}
}

func TestAsyncChannelPathRequiresAction(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "s", "channelPath": "orderBus#/channels/orders", // no action
	}, nil, state)
	if r.Success {
		t.Error("a channelPath step with no action should hard-fail at runtime")
	}
}

func TestAsyncReceiveTimeoutFails(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)
	// receive with nothing sent, small timeout
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "s", "channelPath": "orderBus#/channels/orders", "action": "receive", "timeout": 50,
	}, nil, state)
	if r.Success {
		t.Error("receive on an empty channel should time out and fail")
	}
}

// ---- a send step is not special: outputs and successCriteria must work there too ----

// A send step's `outputs` must be recorded so later steps can reference them — the natural pattern
// being "remember what I published, then correlate the reply on it".
func TestAsyncSendExtractsOutputs(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", map[string]interface{}{"id": "ORD-7"}, nil, nil)

	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"orderId": "$inputs.id"}},
		"outputs": map[string]interface{}{
			"fromInput":   "$inputs.id",
			"fromMessage": "$message.payload.orderId", // the message this step SENT
		},
	}, nil, state)

	if !r.Success {
		t.Fatalf("send failed: %s", r.Error)
	}
	if r.Outputs["fromInput"] != "ORD-7" {
		t.Errorf("output from $inputs: got %v, want ORD-7", r.Outputs["fromInput"])
	}
	if r.Outputs["fromMessage"] != "ORD-7" {
		t.Errorf("output from the sent $message: got %v, want ORD-7", r.Outputs["fromMessage"])
	}
	// and they must be readable by a later step via $steps.emit.outputs.*
	stData, ok := state.StepsData["emit"].(map[string]interface{})
	if !ok || stData["outputs"] == nil {
		t.Fatalf("send outputs must be stored on the step's state, got %v", state.StepsData["emit"])
	}
}

// A send step's declared successCriteria must be evaluated, not ignored.
func TestAsyncSendHonoursSuccessCriteria(t *testing.T) {
	se := newAsyncExecutor()

	// A criterion that cannot hold must fail the step.
	state := models.NewExecutionState("wf", map[string]interface{}{"id": "ORD-7"}, nil, nil)
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody":     map[string]interface{}{"payload": map[string]interface{}{"orderId": "$inputs.id"}},
		"successCriteria": []interface{}{map[string]interface{}{"condition": `$message.payload.orderId == "NOPE"`}},
	}, nil, state)
	if r.Success {
		t.Error("a send step whose successCriteria cannot hold must fail")
	}
	if state.StepsStatus["emit"] != models.StepStatusFailure {
		t.Errorf("failed send should be recorded as failure, got %v", state.StepsStatus["emit"])
	}

	// A criterion that holds must pass.
	state2 := models.NewExecutionState("wf", map[string]interface{}{"id": "ORD-7"}, nil, nil)
	r2 := se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody":     map[string]interface{}{"payload": map[string]interface{}{"orderId": "$inputs.id"}},
		"successCriteria": []interface{}{map[string]interface{}{"condition": `$message.payload.orderId == "ORD-7"`}},
	}, nil, state2)
	if !r2.Success {
		t.Errorf("a send step whose successCriteria hold must pass, got: %s", r2.Error)
	}

	// No criteria at all: a successful publish is still a successful step (unchanged behaviour).
	state3 := models.NewExecutionState("wf", nil, nil, nil)
	r3 := se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"orderId": "X"}},
	}, nil, state3)
	if !r3.Success {
		t.Errorf("a send with no criteria must still succeed, got: %s", r3.Error)
	}
}

// The end-to-end pattern this fix enables: publish, remember what was published, correlate on it.
func TestAsyncSendOutputsDriveCorrelation(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", map[string]interface{}{"id": "ORD-B"}, nil, nil)

	// two messages on the same channel; only the second matches our id
	se.ExecuteStep(map[string]interface{}{
		"stepId": "other", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"orderId": "ORD-A"}},
	}, nil, state)
	se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"orderId": "$inputs.id"}},
		"outputs":     map[string]interface{}{"sentId": "$inputs.id"},
	}, nil, state)

	// correlate the receive on what the send recorded
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "await", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"correlationId": "$steps.emit.outputs.sentId",
		"timeout":       500,
		"outputs":       map[string]interface{}{"got": "$message.payload.orderId"},
	}, nil, state)

	if !r.Success {
		t.Fatalf("correlated receive failed: %s", r.Error)
	}
	if r.Outputs["got"] != "ORD-B" {
		t.Errorf("correlation via the send step's outputs picked the wrong message: got %v, want ORD-B", r.Outputs["got"])
	}
}
