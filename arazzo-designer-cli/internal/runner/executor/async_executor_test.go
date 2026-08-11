package executor

import (
	"strings"
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
	m2, err := a.Receive("ch", "", time.Second)
	if err != nil {
		t.Fatalf("second receive: %v", err) // stop here: m2 is nil on error
	}
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

// ---- correlationId: a declared id must always be honoured ----

// sendThen publishes one order and returns the executor/state ready for a receive step.
func sendThen(t *testing.T, orderID string) (*StepExecutor, *models.ExecutionState) {
	t.Helper()
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)
	se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"orderId": orderID}},
	}, nil, state)
	return se, state
}

// A literal correlationId is a value, not a discarded expression. Previously it evaluated to nil and
// the receive silently went unfiltered — returning an unrelated message and reporting SUCCESS.
func TestAsyncLiteralCorrelationIdIsHonoured(t *testing.T) {
	// A literal that does not match must time out, not return the queued message.
	se, state := sendThen(t, "OP-1")
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "await", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"correlationId": "OP-2", "timeout": 200,
		"outputs": map[string]interface{}{"got": "$message.payload.orderId"},
	}, nil, state)
	if r.Success {
		t.Errorf("a non-matching literal correlationId must not resolve to an unfiltered receive, got %v", r.Outputs["got"])
	}
	if !strings.Contains(r.Error, "OP-2") {
		t.Errorf("the timeout should name the correlation id, got %q", r.Error)
	}

	// A literal that does match must be received.
	se2, state2 := sendThen(t, "OP-1")
	r2 := se2.ExecuteStep(map[string]interface{}{
		"stepId": "await", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"correlationId": "OP-1", "timeout": 500,
		"outputs": map[string]interface{}{"got": "$message.payload.orderId"},
	}, nil, state2)
	if !r2.Success || r2.Outputs["got"] != "OP-1" {
		t.Errorf("a matching literal correlationId should receive the message, got success=%v %v (%s)", r2.Success, r2.Outputs["got"], r2.Error)
	}
}

// An expression that resolves to nothing leaves no id to wait for; the step must fail rather than
// degrade into an unfiltered receive.
func TestAsyncUnresolvableCorrelationIdFails(t *testing.T) {
	se, state := sendThen(t, "OP-1")
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "await", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"correlationId": "$inputs.missing", "timeout": 200,
	}, nil, state)
	if r.Success {
		t.Fatal("an unresolvable correlationId must not fall back to an unfiltered receive")
	}
	if !strings.Contains(r.Error, "resolved to no value") {
		t.Errorf("the failure should explain the unresolved correlationId, got %q", r.Error)
	}
}

// Omitting correlationId is legal and stays unfiltered (the runner warns) — unchanged behaviour.
func TestAsyncNoCorrelationIdStaysUnfiltered(t *testing.T) {
	se, state := sendThen(t, "OP-1")
	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "await", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"timeout": 500, "outputs": map[string]interface{}{"got": "$message.payload.orderId"},
	}, nil, state)
	if !r.Success || r.Outputs["got"] != "OP-1" {
		t.Errorf("a receive with no correlationId should take the next message, got success=%v %v", r.Success, r.Outputs["got"])
	}
}

// ---- telemetry: an async step must be as inspectable in the run logs as a REST step ----

// capturingSink records emitted spans so a test can assert what the run logs will show.
type capturingSink struct{ events []telemetry.TraceEvent }

func (s *capturingSink) Send(e telemetry.TraceEvent) { s.events = append(s.events, e) }
func (s *capturingSink) Shutdown()                   {}

// messageSpans returns only the AsyncAPI messaging spans, in emission order.
func (s *capturingSink) messageSpans() []telemetry.TraceEvent {
	out := []telemetry.TraceEvent{}
	for _, e := range s.events {
		if e.ArazzoKind == telemetry.SpanKindMessage {
			out = append(out, e)
		}
	}
	return out
}

// A send and a receive must each emit a start/end messaging span nested under the step span, the
// same relationship an HTTP span has to its step — carrying the channel, adapter and message.
func TestAsyncEmitsMessagingSpans(t *testing.T) {
	sink := &capturingSink{}
	se := NewStepExecutor(map[string]interface{}{}, asyncSourceDescs(), &models.RuntimeParams{}, sink)
	state := models.NewExecutionState("wf", nil, nil, nil)

	se.ExecuteStep(map[string]interface{}{
		"stepId": "emit", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": map[string]interface{}{"orderId": "A1"}},
	}, nil, state)
	se.ExecuteStep(map[string]interface{}{
		"stepId": "recv", "channelPath": "orderBus#/channels/orders", "action": "receive", "timeout": 500,
	}, nil, state)

	spans := sink.messageSpans()
	if len(spans) != 4 {
		t.Fatalf("expected start+end spans for both send and receive, got %d", len(spans))
	}
	for _, e := range spans {
		if e.ParentID == "" {
			t.Errorf("%s span must be nested under its step span", e.Name)
		}
		if e.Attributes["messaging.channel"] != "orders/new" {
			t.Errorf("%s: channel = %q, want orders/new", e.Name, e.Attributes["messaging.channel"])
		}
		if e.Attributes["messaging.adapter"] != "in-memory" {
			t.Errorf("%s: adapter = %q, want in-memory", e.Name, e.Attributes["messaging.adapter"])
		}
	}

	// The send carries the published message up front (its "request" side).
	if got := spans[0].Attributes["messaging.message.body"]; got != `{"orderId":"A1"}` {
		t.Errorf("send start should carry the published payload, got %q", got)
	}
	if spans[1].StatusCode != telemetry.SpanStatusOK {
		t.Errorf("successful send should end OK, got %s", spans[1].StatusCode)
	}
	// The receive declares what it is waiting for, then reports what arrived (its "response" side).
	if got := spans[2].Attributes["messaging.timeout_ms"]; got != "500" {
		t.Errorf("receive start should record the timeout, got %q", got)
	}
	if got := spans[3].Attributes["messaging.message.body"]; got != `{"orderId":"A1"}` {
		t.Errorf("receive end should carry the received payload, got %q", got)
	}
}

// A timed-out receive must close its span as an error carrying the reason, so the run logs explain
// the failure rather than just showing a red step.
func TestAsyncTimeoutRecordedOnMessagingSpan(t *testing.T) {
	sink := &capturingSink{}
	se := NewStepExecutor(map[string]interface{}{}, asyncSourceDescs(), &models.RuntimeParams{}, sink)
	state := models.NewExecutionState("wf", nil, nil, nil)

	se.ExecuteStep(map[string]interface{}{
		"stepId": "recv", "channelPath": "orderBus#/channels/orders", "action": "receive", "timeout": 50,
	}, nil, state)

	spans := sink.messageSpans()
	if len(spans) != 2 {
		t.Fatalf("expected a start+end span, got %d", len(spans))
	}
	end := spans[1]
	if end.StatusCode != telemetry.SpanStatusError {
		t.Errorf("a timed-out receive should end as an error, got %s", end.StatusCode)
	}
	if !strings.Contains(end.StatusMessage, "timed out") {
		t.Errorf("the span should explain the timeout, got %q", end.StatusMessage)
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

// ---- Phase 10: serialization wiring through ExecuteStep ----

// A message delivered as raw bytes only (the shape a real broker produces) is deserialized via the
// content type into $message.payload — the in-memory adapter carries Payload, so we seed Raw-only.
func TestAsyncReceiveDeserializesRawBytes(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)

	// Seed a bytes-only message directly on the channel address ("orders/new") — no decoded Payload.
	_ = se.AsyncAdapter.Send("orders/new", &Message{
		ContentType: "application/json",
		Raw:         []byte(`{"orderId":"Z9","status":"new"}`),
	})

	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "recv", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"successCriteria": []interface{}{map[string]interface{}{"condition": `$message.payload.status == "new"`}},
		"outputs":         map[string]interface{}{"id": "$message.payload.orderId"},
	}, nil, state)
	if !r.Success {
		t.Fatalf("receive of raw-only message failed: %s", r.Error)
	}
	if r.Outputs["id"] != "Z9" {
		t.Errorf("expected deserialized payload orderId=Z9, got %v", r.Outputs["id"])
	}
}

// A text/plain send serializes the payload as raw text (not JSON-quoted).
func TestAsyncSendUsesContentTypeSerializer(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)

	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{
			"contentType": "text/plain",
			"payload":     "hello world",
		},
	}, nil, state)
	if !r.Success {
		t.Fatalf("text/plain send failed: %s", r.Error)
	}

	// Pull the raw bytes back off the adapter and confirm they are plain text, not JSON.
	msg, err := se.AsyncAdapter.Receive("orders/new", "", time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if string(msg.Raw) != "hello world" {
		t.Errorf("text/plain send should produce raw text %q, got %q", "hello world", msg.Raw)
	}
	if msg.ContentType != "text/plain" {
		t.Errorf("expected contentType text/plain, got %q", msg.ContentType)
	}
}

// An unsupported content type fails loudly at send instead of guessing a wire format.
func TestAsyncSendUnsupportedContentTypeFails(t *testing.T) {
	se := newAsyncExecutor()
	state := models.NewExecutionState("wf", nil, nil, nil)

	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{
			"contentType": "application/octet-stream",
			"payload":     map[string]interface{}{"x": 1},
		},
	}, nil, state)
	if r.Success {
		t.Error("send with an unsupported content type should fail clearly")
	}
}

// ---- content type resolution: step -> AsyncAPI document -> JSON ----

// declaredContentTypeSources is asyncSourceDescs with the AsyncAPI document declaring a content type,
// either on the channel's message or (when defaultOnly) only at the document root.
func declaredContentTypeSources(contentType string, defaultOnly bool) map[string]interface{} {
	channel := map[string]interface{}{"address": "orders/new"}
	spec := map[string]interface{}{
		"asyncapi": "3.0.0",
		"channels": map[string]interface{}{"orders": channel},
		"operations": map[string]interface{}{
			"placeOrder": map[string]interface{}{
				"action":  "send",
				"channel": map[string]interface{}{"$ref": "#/channels/orders"},
			},
		},
	}
	if defaultOnly {
		spec["defaultContentType"] = contentType
	} else {
		channel["messages"] = map[string]interface{}{
			"order": map[string]interface{}{"contentType": contentType},
		}
	}
	return map[string]interface{}{"orderBus": spec}
}

func executorWithSources(sources map[string]interface{}) *StepExecutor {
	return NewStepExecutor(map[string]interface{}{}, sources, &models.RuntimeParams{}, &telemetry.NoopSink{})
}

// sendRaw publishes a payload and returns the raw bytes that reached the channel — the bytes a real
// broker would carry, which is what the content type actually decides.
func sendRaw(t *testing.T, se *StepExecutor, step map[string]interface{}) []byte {
	t.Helper()
	state := models.NewExecutionState("wf", nil, nil, nil)
	if r := se.ExecuteStep(step, nil, state); !r.Success {
		t.Fatalf("send failed: %s", r.Error)
	}
	msg, err := se.AsyncAdapter.Receive("orders/new", "", time.Second)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return msg.Raw
}

// Arazzo §5.8.14.1: a request body with no `contentType` must "refer to Content-Type specified at the
// targeted operation" — for an async step, the AsyncAPI document. Defaulting straight to JSON would
// publish `"hi"` (JSON-quoted) onto a channel documented as carrying bare text.
func TestSendFallsBackToAsyncAPIDeclaredContentType(t *testing.T) {
	se := executorWithSources(declaredContentTypeSources("text/plain", false))
	raw := sendRaw(t, se, map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": "hi"}, // no contentType on the step
	})
	if string(raw) != "hi" {
		t.Errorf("channel declares text/plain, so a step omitting contentType should send bare text; got %q", raw)
	}
}

// AsyncAPI 3.0 Message Object: "When omitted, the value MUST be the one specified on the
// defaultContentType field" — so a document-level default counts as declared.
func TestSendFallsBackToDocumentDefaultContentType(t *testing.T) {
	se := executorWithSources(declaredContentTypeSources("text/plain", true))
	raw := sendRaw(t, se, map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": "hi"},
	})
	if string(raw) != "hi" {
		t.Errorf("root defaultContentType text/plain should apply; got %q", raw)
	}
}

// The step's own contentType is authoritative: the spec consults the target only when it is OMITTED.
func TestSendStepContentTypeWinsOverDeclared(t *testing.T) {
	se := executorWithSources(declaredContentTypeSources("text/plain", false))
	raw := sendRaw(t, se, map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"contentType": "application/json", "payload": "hi"},
	})
	if string(raw) != `"hi"` {
		t.Errorf("the step's contentType must win over the document's; got %q", raw)
	}
}

// Nothing declared anywhere still means JSON.
func TestSendDefaultsToJSONWhenNothingDeclared(t *testing.T) {
	se := newAsyncExecutor()
	raw := sendRaw(t, se, map[string]interface{}{
		"stepId": "send", "channelPath": "orderBus#/channels/orders", "action": "send",
		"requestBody": map[string]interface{}{"payload": "hi"},
	})
	if string(raw) != `"hi"` {
		t.Errorf("with no contentType anywhere the default is JSON; got %q", raw)
	}
}

// The real-broker receive path: bytes arrive with no content type (MQTT 3.1.1 / WebSocket carry none),
// so the AsyncAPI declaration is the only thing that says how to decode them.
func TestReceiveUsesDeclaredContentTypeForRawBytes(t *testing.T) {
	se := executorWithSources(declaredContentTypeSources("text/plain", false))
	state := models.NewExecutionState("wf", nil, nil, nil)

	// Bytes only — exactly what a real broker delivers.
	_ = se.AsyncAdapter.Send("orders/new", &Message{Raw: []byte("plain words")})

	r := se.ExecuteStep(map[string]interface{}{
		"stepId": "recv", "channelPath": "orderBus#/channels/orders", "action": "receive",
		"timeout": 500,
		"outputs": map[string]interface{}{"got": "$message.payload"},
	}, nil, state)
	if !r.Success {
		t.Fatalf("receive failed: %s", r.Error)
	}
	if r.Outputs["got"] != "plain words" {
		t.Errorf("channel declares text/plain, so raw bytes should decode as text; got %v", r.Outputs["got"])
	}
}
