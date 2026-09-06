// async_telemetry.go gives AsyncAPI steps the same run-log visibility REST steps already have.
//
// A REST step emits a child span from the HTTP executor (SpanKindHTTP) carrying the request and the
// response, which is what the Logs tab renders under the step. The async path had no equivalent, so
// an async step showed only its generic step span — you could see that it ran, but not what it
// published, what it received, or why it failed.
//
// This brackets one adapter Send/Receive with a start/end span pair in exactly the same shape the
// HTTP executor uses: request-side details on the start event, result details on the end event.
// Telemetry stays HERE rather than inside the Adapter implementations, so adapters remain pure
// transport and every future broker adapter is instrumented for free.
package executor

import (
	"encoding/json"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// messagingSpan is one in-flight AsyncAPI operation span. Create it with startMessagingSpan and
// close it with end (mirrors the httpStart/httpEnd pair in the HTTP executor).
type messagingSpan struct {
	se       *StepExecutor
	traceID  string
	parentID string
	spanID   string
	name     string
	start    time.Time
	attrs    map[string]string
}

// startMessagingSpan emits the start event for a send/receive and returns the handle used to close
// it. operation is "send" or "receive"; channel is the broker-side address the adapter acts on.
func (se *StepExecutor) startMessagingSpan(traceID, parentSpanID, operation, channel, adapterName string, attrs map[string]string) *messagingSpan {
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["messaging.operation"] = operation
	attrs["messaging.channel"] = channel
	attrs["messaging.adapter"] = adapterName

	s := &messagingSpan{
		se:       se,
		traceID:  traceID,
		parentID: parentSpanID,
		spanID:   telemetry.GenerateSpanID(),
		// Reads like the HTTP span's "POST https://..." in the logs.
		name:  upperOperation(operation) + " " + channel,
		start: time.Now(),
		attrs: attrs,
	}

	se.Sink.Send(telemetry.TraceEvent{
		Lifecycle:  telemetry.LifecycleStart,
		Context:    telemetry.SpanContext{TraceID: s.traceID, SpanID: s.spanID},
		ParentID:   s.parentID,
		Name:       s.name,
		Kind:       telemetry.OTelSpanKindClient,
		ArazzoKind: telemetry.SpanKindMessage,
		StartTime:  s.start,
		StatusCode: telemetry.SpanStatusUnset,
		Attributes: copyAttrs(s.attrs),
	})
	return s
}

// end closes the span with an outcome. extra carries result-side details (the received message, the
// failure reason) the same way the HTTP end span carries the response.
func (s *messagingSpan) end(status telemetry.SpanStatus, statusMessage string, extra map[string]string) {
	if s == nil {
		return
	}
	dur := float64(time.Since(s.start).Milliseconds())
	endTime := time.Now()

	attrs := copyAttrs(s.attrs)
	for k, v := range extra {
		attrs[k] = v
	}

	s.se.Sink.Send(telemetry.TraceEvent{
		Lifecycle:     telemetry.LifecycleEnd,
		Context:       telemetry.SpanContext{TraceID: s.traceID, SpanID: s.spanID},
		ParentID:      s.parentID,
		Name:          s.name,
		Kind:          telemetry.OTelSpanKindClient,
		ArazzoKind:    telemetry.SpanKindMessage,
		StartTime:     s.start,
		EndTime:       &endTime,
		DurationMs:    &dur,
		StatusCode:    status,
		StatusMessage: statusMessage,
		Attributes:    attrs,
	})
}

// messageAttrs renders a message's payload and headers as span attributes, mirroring the HTTP
// executor's http.request.body / http.response.body.
func messageAttrs(prefix string, payload interface{}, headers map[string]interface{}) map[string]string {
	attrs := map[string]string{}
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			attrs[prefix+".body"] = string(b)
		}
	}
	if len(headers) > 0 {
		if b, err := json.Marshal(headers); err == nil {
			attrs[prefix+".headers"] = string(b)
		}
	}
	return attrs
}

// copyAttrs returns a shallow copy so the start and end events never share a mutable map.
func copyAttrs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// upperOperation renders the operation for the span name ("send" -> "SEND"), matching the HTTP
// span's upper-cased method.
func upperOperation(operation string) string {
	switch operation {
	case "send":
		return "SEND"
	case "receive":
		return "RECEIVE"
	default:
		return operation
	}
}
