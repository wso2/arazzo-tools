// async_executor.go wires AsyncAPI send/receive steps into the runner (Phase 9). It resolves the
// target with the Phase-8 AsyncFinder, then Sends or Receives via the configured Adapter. It reuses
// the existing ParameterProcessor (build payload/headers), SuccessCriteriaChecker, and OutputExtractor
// — feeding them $message instead of an HTTP $response — so async and HTTP steps evaluate criteria and
// outputs through the same code.
package executor

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/evaluator"
	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// resolveAsyncTarget reports whether a step is an AsyncAPI step and, if so, its resolved target.
// A step is async when it has a `channelPath`, or an `operationId`/`operationPath` that resolves to an
// AsyncAPI operation — all three targeting forms the spec allows, matching what the LSP already
// navigates and validates.
//
// An `operationId`/`operationPath` that does NOT resolve to an AsyncAPI operation falls through to the
// HTTP path, which is how REST steps using those same fields keep working (OpenAPI operationIds live
// under `paths` rather than `operations`, and an OpenAPI operation has no `action`). For a channelPath
// the step is always treated as async even if resolution fails (nil info), so a malformed channel
// hard-fails rather than silently being executed as HTTP.
func (se *StepExecutor) resolveAsyncTarget(step map[string]interface{}) (*AsyncInfo, bool) {
	finder := NewAsyncFinder(se.SourceDescriptions)
	if cp, _ := step["channelPath"].(string); strings.TrimSpace(cp) != "" {
		return finder.FindChannelByPath(cp), true
	}
	if opID, _ := step["operationId"].(string); strings.TrimSpace(opID) != "" {
		if info := finder.FindOperationByID(opID); info != nil {
			return info, true
		}
	}
	if opPath, _ := step["operationPath"].(string); strings.TrimSpace(opPath) != "" {
		if info := finder.FindOperationByPath(opPath); info != nil {
			return info, true
		}
	}
	return nil, false
}

// executeAsyncStep runs a resolved AsyncAPI step (send or receive) against the configured adapter.
// parentSpanID is the step's span, under which the messaging span is nested — the same relationship
// the HTTP span has to its step.
func (se *StepExecutor) executeAsyncStep(step map[string]interface{}, info *AsyncInfo, state *models.ExecutionState, stepID, parentSpanID string) *models.StepResult {
	if info == nil {
		return se.createFailureResult(stepID, step, state, "AsyncAPI target could not be resolved (channel or operation not found)")
	}
	// Pick the transport from the AsyncAPI `servers` declaration (Phase 11): ws/mqtt -> real broker
	// adapter, no servers -> the default in-memory adapter.
	adapter, err := se.adapterFor(info)
	if err != nil {
		return se.createFailureResult(stepID, step, state, err.Error())
	}
	if adapter == nil {
		return se.createFailureResult(stepID, step, state, "AsyncAPI execution requires a configured adapter for this protocol")
	}

	action, err := resolveAsyncAction(step, info)
	if err != nil {
		return se.createFailureResult(stepID, step, state, err.Error())
	}
	channel := info.ChannelAddress
	if channel == "" {
		channel = info.ChannelKey
	}
	if channel == "" {
		return se.createFailureResult(stepID, step, state, "AsyncAPI step has no resolvable channel")
	}

	switch action {
	case "send":
		return se.executeSend(step, adapter, info, channel, state, stepID, parentSpanID)
	case "receive":
		return se.executeReceive(step, adapter, info, channel, state, stepID, parentSpanID)
	default:
		return se.createFailureResult(stepID, step, state, fmt.Sprintf("invalid AsyncAPI action %q", action))
	}
}

// PrewarmAsyncChannels subscribes, BEFORE the workflow's first step runs, to every channel that some
// step will receive on.
//
// Subscription is otherwise lazy — it happens inside the first Send or Receive that touches a channel.
// That leaves a window: a message a peer publishes to a channel between the run starting and the step
// that reads it being reached is gone, because a broker does not replay what arrived while nobody was
// listening. Walking the steps up front closes the window to the workflow's own start, which is as
// early as this layer can get.
//
// Only channels a step RECEIVES on are warmed. Subscribing fills a channel's buffer, and the only
// thing that drains it is a receive; a send-only channel would accumulate every message published
// there for the whole run with nothing ever reading them. Sends are unaffected — Send still subscribes
// before it publishes (so a same-workflow round trip works), and any channel in a send/receive pair
// has a receive step and is therefore warmed anyway.
//
// Failures are WARNINGS, never fatal. A channel that cannot be reached now will be retried by the step
// that needs it, which fails with its own precise error; and a channel behind a branch the run never
// takes must not sink a workflow that would otherwise have succeeded. This runs before any span
// exists, so it is a plain log line and never becomes step telemetry.
func (se *StepExecutor) PrewarmAsyncChannels(steps []interface{}) {
	warmed := map[string]bool{}
	var listening []string

	for _, raw := range steps {
		step := toMap(raw)
		if step == nil {
			continue
		}
		info, isAsync := se.resolveAsyncTarget(step)
		if !isAsync || info == nil {
			continue
		}
		if asyncDirection(step, info) != "receive" {
			continue
		}

		channel := info.ChannelAddress
		if channel == "" {
			channel = info.ChannelKey
		}
		if channel == "" {
			continue // no resolvable channel; the step will report this properly when it runs
		}

		stepID, _ := step["stepId"].(string)
		adapter, err := se.adapterFor(info)
		if err != nil || adapter == nil {
			continue // unsupported protocol or no adapter — the step itself reports it
		}
		// One subscription per adapter+channel: several steps commonly read the same channel.
		key := adapter.Name() + "\x00" + channel
		if warmed[key] {
			continue
		}
		warmed[key] = true

		if err := adapter.Subscribe(channel); err != nil {
			log.Printf("Warning: could not subscribe to channel %q via the %s adapter before the workflow started (needed by step %s): %v — the step will try again when it runs, and messages published in the meantime are lost",
				channel, adapter.Name(), stepID, err)
			continue
		}
		listening = append(listening, channel)
	}

	// Say what is being listened to, so a successful prewarm is visible rather than merely implied.
	// Named channels only: which adapter carries each is already on every step's own log line.
	if len(listening) > 0 {
		log.Printf("Listening on %d channel(s) before the first step: %s", len(listening), strings.Join(quoteAll(listening), ", "))
	}
}

// asyncDirection reports the direction a step will run with, WITHOUT the validation and warnings
// resolveAsyncAction emits. Prewarm inspects every step before any of them execute, so it must not
// pre-empt or duplicate the diagnostics a step produces when it actually runs. Mirrors the same
// precedence: a targeted operation's declared action wins over the step's own.
func asyncDirection(step map[string]interface{}, info *AsyncInfo) string {
	if info != nil && info.Action != "" {
		return info.Action
	}
	action, _ := step["action"].(string)
	return strings.TrimSpace(action)
}

// resolveAsyncAction determines the direction (send/receive) of an async step. When the step targets
// an operation, the operation's declared action wins (spec/Phase-8 decision) and a contradicting step
// `action` only produces a warning. When the step targets a channel (which has no direction), the step
// MUST declare `action` — otherwise it is a hard error.
func resolveAsyncAction(step map[string]interface{}, info *AsyncInfo) (string, error) {
	stepAction, _ := step["action"].(string)
	stepAction = strings.TrimSpace(stepAction)
	opAction := info.Action

	if opAction != "" { //operationID is given so we have a acion from the asyncAPI file
		if stepAction != "" && stepAction != opAction { //if a step action is given then it must match with the asyncAPI file action
			log.Printf("Warning: step action %q contradicts the AsyncAPI operation's action %q; using the operation's action", stepAction, opAction)
		}
		return opAction, nil
	}
	if stepAction == "" { //if we reach here that means that a channelPath is given. if there is no action then there is an error
		return "", fmt.Errorf("a 'channelPath' step requires 'action' (send or receive) — the message-flow direction is otherwise undefined")
	}
	if stepAction != "send" && stepAction != "receive" {
		return "", fmt.Errorf("invalid action %q (must be 'send' or 'receive')", stepAction)
	}
	return stepAction, nil
}

// executeSend builds the outgoing message from the step's requestBody + header parameters, serializes
// it to wire bytes via the Phase-10 serializer chosen by the resolved content type, publishes it on
// the channel via the selected adapter, then — exactly like the receive path — evaluates any
// `successCriteria` and extracts any `outputs` through the shared checker/extractor.
func (se *StepExecutor) executeSend(step map[string]interface{}, adapter Adapter, info *AsyncInfo, channel string, state *models.ExecutionState, stepID, parentSpanID string) *models.StepResult {
	var payload interface{}
	stepContentType := ""
	if reqBody := toMap(step["requestBody"]); reqBody != nil {
		if prepared := se.ParamProcessor.PrepareRequestBody(reqBody, state); prepared != nil {
			payload = prepared["payload"]
			stepContentType, _ = prepared["contentType"].(string)
		}
	}
	contentType := resolveSendContentType(stepContentType, info, stepID, channel)
	headers := headerParams(se.ParamProcessor.PrepareParameters(step, state))

	// Serialize the logical payload into the wire form the channel carries (Phase 10). A payload that
	// cannot be encoded must not be published as an empty message and reported as a successful send.
	serializer, err := se.serializerRegistry().For(contentType)
	if err != nil {
		return se.createFailureResult(stepID, step, state, fmt.Sprintf("send on channel %q: %v", channel, err))
	}
	raw, err := serializer.Serialize(payload)
	if err != nil {
		return se.createFailureResult(stepID, step, state, fmt.Sprintf("send on channel %q: could not serialize payload: %v", channel, err))
	}
	// Publish the content type as RESOLVED, not the serializer's canonical name: a vendor type
	// ("application/vnd.order+json") or a charset parameter is information the receiver may care about,
	// and canonicalizing would quietly discard it. Fall back to the serializer's own type when nothing
	// was declared anywhere, so the message still says what format it is.
	publishedContentType := contentType
	if strings.TrimSpace(publishedContentType) == "" {
		publishedContentType = serializer.ContentType()
	}
	msg := &Message{
		Payload:     payload,
		Headers:     headers,
		ContentType: publishedContentType,
		Raw:         raw,
		Metadata:    map[string]interface{}{},
	}

	// The message being published is the request-side detail of this span, mirroring how the HTTP
	// span carries the request body. The encoder that produced the bytes goes with it — which
	// serializer ran is the one thing Phase 10 decides, and it is invisible from the payload alone.
	sendAttrs := messageAttrs("messaging.message", payload, headers)
	sendAttrs["messaging.content_type"] = msg.ContentType
	span := se.startMessagingSpan(state.TraceID, parentSpanID, "send", channel, adapter.Name(), sendAttrs)

	if err := adapter.Send(channel, msg); err != nil {
		span.end(telemetry.SpanStatusError, err.Error(), nil)
		return se.createFailureResult(stepID, step, state, fmt.Sprintf("send on channel %q failed: %v", channel, err))
	}

	// A send step is NOT special: like every other step it may declare `successCriteria` and
	// `outputs`, and both run through the SAME checker/extractor the HTTP and receive paths use —
	// declaring either must never be silently ignored. The message this step SENT is exposed as
	// $message (shaped {header, payload}), the natural counterpart of the received message on the
	// receive path, so a step can record what it published for a later step to correlate on.
	message := map[string]interface{}{
		"header":  headers,
		"payload": payload,
	}
	responseForCheck := map[string]interface{}{"message": message}

	state.StepsData[stepID] = map[string]interface{}{
		"async":   map[string]interface{}{"action": "send", "channel": channel, "payload": payload},
		"message": message,
	}

	// No successCriteria means a successful publish is a successful step; otherwise the declared
	// criteria decide.
	success := true
	if len(toSlice(step["successCriteria"])) > 0 {
		success = se.SuccessChecker.CheckSuccessCriteria(step, responseForCheck, state)
	}
	if success {
		state.StepsStatus[stepID] = models.StepStatusSuccess
		span.end(telemetry.SpanStatusOK, "", nil)
		// Log the wire form, not just the fact of sending: which serializer ran is the whole subject of
		// the content-type resolution above, and it is otherwise invisible — the in-memory adapter
		// carries the decoded payload alongside the bytes, so a receive step never has to decode and
		// every format looks identical from the workflow's outputs.
		log.Printf("Step %s: sent a message on channel %q via %s adapter as %s (%d bytes): %s",
			stepID, channel, adapter.Name(), msg.ContentType, len(raw), previewBytes(raw))
	} else {
		state.StepsStatus[stepID] = models.StepStatusFailure
		span.end(telemetry.SpanStatusError, "sent message did not satisfy successCriteria", nil)
		log.Printf("Step %s: sent a message on channel %q but successCriteria failed", stepID, channel)
	}

	// Extract outputs (same extractor as the HTTP/receive paths; $message resolves via the context).
	outputs := se.OutputExtractor.ExtractOutputs(step, responseForCheck, state)
	if outputs != nil {
		if stData, ok := state.StepsData[stepID].(map[string]interface{}); ok {
			stData["outputs"] = outputs
		}
	}

	failureReason := ""
	if !success {
		failureReason = "sent message did not satisfy successCriteria"
	}

	return &models.StepResult{
		StepID:       stepID,
		Success:      success,
		ResponseBody: payload,
		Outputs:      outputs,
		Error:        failureReason,
		NextAction:   se.ActionHandler.DetermineNextAction(step, success, state),
	}
}

// resolveSendContentType applies the Arazzo rule for a request body's content type: the step's own
// `contentType` wins, and only when it is omitted does the runtime "refer to Content-Type specified at
// the targeted operation" (§5.8.14.1) — for an async step, the content type the AsyncAPI document
// declares for the targeted channel/message. JSON remains the last resort (an empty content type
// selects the registry's default), not the first answer.
//
// Both values present and disagreeing is not an error: the step's value is authoritative per the rule
// above. It is worth a warning though, because the AsyncAPI document is the contract other consumers
// of the channel read, and publishing a format it doesn't describe is usually a mistake.
func resolveSendContentType(stepContentType string, info *AsyncInfo, stepID, channel string) string {
	declared := info.DeclaredContentTypes()

	if strings.TrimSpace(stepContentType) == "" {
		if len(declared) == 0 {
			return "" // nothing declared anywhere: the registry's default (JSON) applies
		}
		// More than one declared format means the channel carries messages of different kinds and the
		// document does not say which one THIS step sends — the first is a deterministic pick, not an
		// answer. Only worth saying when the step declared nothing itself: a step that did has already
		// settled it, and telling it to "set contentType" would be advice it has followed.
		if len(declared) > 1 {
			log.Printf("Warning: step %s: channel %q declares more than one contentType (%s); using %q — set 'contentType' on the step's requestBody to choose explicitly",
				stepID, channel, strings.Join(declared, ", "), declared[0])
		}
		return declared[0]
	}

	// A disagreement is only a disagreement when the step's format is NOT one the document declares.
	// Comparing against a single pick would falsely flag a step that correctly named the channel's
	// SECOND message format.
	if len(declared) > 0 && !containsMediaType(declared, stepContentType) {
		log.Printf("Warning: step %s: requestBody contentType %q differs from the %s declared by the AsyncAPI document for channel %q; the value declared in this step overrides the AsyncAPI declaration",
			stepID, stepContentType, strings.Join(quoteAll(declared), " / "), channel)
	}
	return stepContentType
}

// quoteAll renders content types for a message, each in quotes.
func quoteAll(types []string) []string {
	out := make([]string, len(types))
	for i, t := range types {
		out[i] = fmt.Sprintf("%q", t)
	}
	return out
}

// executeReceive waits for a (optionally correlated) message on the channel, exposes it as $message,
// then evaluates successCriteria and extracts outputs through the SAME checker/extractor used by HTTP
// steps. A received message with no successCriteria counts as success.
func (se *StepExecutor) executeReceive(step map[string]interface{}, adapter Adapter, info *AsyncInfo, channel string, state *models.ExecutionState, stepID, parentSpanID string) *models.StepResult {
	// Resolve the correlation id. A DECLARED correlationId must always be honoured: silently falling
	// back to an unfiltered receive would return an unrelated message and report success, which is
	// worse than failing. Only the ABSENCE of a correlationId means "take the next message".
	correlationID, corrErr := se.resolveCorrelationID(step, state, stepID, channel)
	if corrErr != "" {
		return se.createFailureResult(stepID, step, state, corrErr)
	}

	timeout := receiveTimeout(step)
	correlation := se.resolveCorrelation(correlationID, info, stepID, channel)

	// What we are waiting FOR is the request-side detail of this span (the analogue of the HTTP
	// request); the message that actually arrives is added on the end event below.
	waitAttrs := map[string]string{"messaging.timeout_ms": fmt.Sprintf("%d", timeout.Milliseconds())}
	if correlationID != "" {
		waitAttrs["messaging.correlation_id"] = correlationID
		// Which places were consulted is as much a part of "how this message was chosen" as the id
		// itself, and it is the difference between a precise match and a whole-message scan.
		if len(correlation.Locations) > 0 {
			waitAttrs["messaging.correlation_location"] = strings.Join(correlation.Locations, ", ")
		}
	}
	span := se.startMessagingSpan(state.TraceID, parentSpanID, "receive", channel, adapter.Name(), waitAttrs)

	msg, err := adapter.Receive(channel, correlation, timeout)
	if err != nil {
		reason := fmt.Sprintf("receive on channel %q failed: %v", channel, err)
		if errors.Is(err, ErrReceiveTimeout) {
			if correlationID != "" {
				reason = fmt.Sprintf("receive on channel %q timed out after %s: no message matching correlationId %q arrived", channel, timeout, correlationID)
			} else {
				reason = fmt.Sprintf("receive on channel %q timed out after %s: no message arrived", channel, timeout)
			}
		}
		span.end(telemetry.SpanStatusError, reason, nil)
		return se.createFailureResult(stepID, step, state, reason)
	}

	// Decode the wire bytes into a payload when the adapter delivered only Raw (Phase 10). The
	// in-memory test adapter already carries the decoded Payload, so this is a no-op there; a real
	// broker delivers bytes and this is where they become $message.payload.
	//
	// A receive step has no requestBody, so there is no step-level content type to consult. The
	// transport speaks first when it can (HTTP has a Content-Type header, MQTT 5 a Content Type
	// property), but MQTT 3.1.1 and WebSocket carry no such field at all — for those the AsyncAPI
	// document is the only thing that says how to read the bytes. JSON stays the last resort.
	//
	// Resolved up front rather than inside the decode branch so the format can be REPORTED even when
	// no decode was needed: with the in-memory adapter the payload arrives already decoded, and a user
	// still needs to see which decoder governs the channel.
	contentType := strings.TrimSpace(msg.ContentType)
	if contentType == "" {
		// The transport said nothing, so the document decides — and if it declares several formats it
		// cannot say which one THIS message is. Unlike a send, there is nothing on the step to settle it
		// with (a receive has no requestBody), so the fix is in the AsyncAPI document. Same shape as the
		// send-side warning in resolveSendContentType.
		declared := info.DeclaredContentTypes()
		if len(declared) > 1 {
			log.Printf("Warning: step %s: the message carried no contentType and channel %q declares more than one (%s); decoding as %q — declare one format per channel so the decoder is unambiguous",
				stepID, channel, strings.Join(declared, ", "), declared[0])
		}
		if len(declared) > 0 {
			contentType = declared[0]
		}
	}
	// One lookup, used both to name the format the way the registry does (which also turns "nothing
	// declared" into the JSON default) and to decode below.
	serializer, serr := se.serializerRegistry().For(contentType)
	if serr == nil {
		contentType = serializer.ContentType()
	}

	payload := msg.Payload
	if payload == nil && len(msg.Raw) > 0 {
		if serr != nil {
			reason := fmt.Sprintf("receive on channel %q: cannot decode message: %v", channel, serr)
			span.end(telemetry.SpanStatusError, reason, nil)
			return se.createFailureResult(stepID, step, state, reason)
		}
		decoded, derr := serializer.Deserialize(msg.Raw)
		if derr != nil {
			reason := fmt.Sprintf("receive on channel %q: could not deserialize message body: %v", channel, derr)
			span.end(telemetry.SpanStatusError, reason, nil)
			return se.createFailureResult(stepID, step, state, reason)
		}
		payload = decoded
	}

	// Shape the message for the evaluator's $message root: {header, payload}.
	message := map[string]interface{}{
		"header":  msg.Headers,
		"payload": payload,
	}
	// The reused SuccessChecker / OutputExtractor read the message from response["message"].
	responseForCheck := map[string]interface{}{"message": message}

	state.StepsData[stepID] = map[string]interface{}{
		"async":   map[string]interface{}{"action": "receive", "channel": channel},
		"message": message,
	}

	// A received message with no successCriteria is a success; otherwise reuse the criteria checker.
	success := true
	if len(toSlice(step["successCriteria"])) > 0 {
		success = se.SuccessChecker.CheckSuccessCriteria(step, responseForCheck, state)
	}
	// The message that arrived is the result-side detail, mirroring the HTTP response body. The
	// decoder is reported alongside it: which format governed the message is otherwise invisible.
	receivedAttrs := messageAttrs("messaging.message", payload, msg.Headers)
	receivedAttrs["messaging.content_type"] = contentType
	if success {
		state.StepsStatus[stepID] = models.StepStatusSuccess
		span.end(telemetry.SpanStatusOK, "", receivedAttrs)
		log.Printf("Step %s: received a message on channel %q via %s adapter, decoded as %s",
			stepID, channel, adapter.Name(), contentType)
	} else {
		state.StepsStatus[stepID] = models.StepStatusFailure
		span.end(telemetry.SpanStatusError, "received message did not satisfy successCriteria", receivedAttrs)
		log.Printf("Step %s: received a message on channel %q but successCriteria failed", stepID, channel)
	}

	// Extract outputs (reuses the HTTP extractor; $message resolves via the shared context).
	outputs := se.OutputExtractor.ExtractOutputs(step, responseForCheck, state)
	if outputs != nil {
		if stData, ok := state.StepsData[stepID].(map[string]interface{}); ok {
			stData["outputs"] = outputs
		}
	}

	failureReason := ""
	if !success {
		failureReason = "received message did not satisfy successCriteria"
	}

	return &models.StepResult{
		StepID:       stepID,
		Success:      success,
		ResponseBody: payload,
		Outputs:      outputs,
		Error:        failureReason,
		NextAction:   se.ActionHandler.DetermineNextAction(step, success, state),
	}
}

// resolveCorrelationID works out which correlation id a receive step should match on. It returns the
// id (empty means "unfiltered"), or a non-empty failure reason.
//
// Three cases, and the distinction matters:
//   - NOT DECLARED  -> unfiltered receive (the next message on the channel), with a warning, because
//     that is easy to do by accident and quietly returns whatever happens to be queued.
//   - A RUNTIME EXPRESSION ("$inputs.token", "{$steps.x.outputs.id}") -> its resolved value. If it
//     resolves to nothing there is no id to wait for, so the step FAILS rather than degrading to an
//     unfiltered receive that would happily return an unrelated message and report success.
//   - ANYTHING ELSE -> a literal id, used as written. `correlationId` is typed `string` in the spec;
//     the spec's example uses an expression, but a literal is a perfectly ordinary value and must not
//     be silently discarded.
func (se *StepExecutor) resolveCorrelationID(step map[string]interface{}, state *models.ExecutionState, stepID, channel string) (correlationID, failure string) {
	raw, present := step["correlationId"]
	corrExpr := ""
	if present && raw != nil {
		corrExpr = strings.TrimSpace(fmt.Sprintf("%v", raw))
	}

	if corrExpr == "" {
		log.Printf("Warning: step %s: receive on channel %q declares no correlationId — it will consume the next message on the channel without filtering, which may be a message this workflow did not expect", stepID, channel)
		return "", ""
	}

	// Same runtime-expression test the parameter processor uses.
	if strings.HasPrefix(corrExpr, "$") || strings.Contains(corrExpr, "{$") {
		v := evaluator.EvaluateExpression(corrExpr, state, se.SourceDescriptions, nil)
		if v == nil {
			return "", fmt.Sprintf("receive on channel %q: correlationId %q resolved to no value, so there is no id to match — refusing to fall back to an unfiltered receive", channel, corrExpr)
		}
		return fmt.Sprintf("%v", v), ""
	}

	return corrExpr, ""
}

// resolveCorrelation pairs the id a step is waiting for with the places the AsyncAPI document says
// that id lives, producing the whole instruction the adapter needs to pick a message.
//
// A declared location turns matching from a guess into a lookup. Without one the matcher has to search
// the entire message — metadata, headers, every scalar in the payload, and finally the raw bytes as a
// substring — which can match a message that merely MENTIONS the id: waiting for "42" against a body
// reading {"orderId":"99","note":"see ticket 42"} succeeds, and the workflow proceeds on the wrong
// message while reporting success. That is exactly what the AsyncAPI Correlation ID Object exists to
// prevent, so when the document declares one it is used exclusively, and when it does not the receive
// says so rather than leaving the imprecision invisible (the editor reports the same thing).
func (se *StepExecutor) resolveCorrelation(correlationID string, info *AsyncInfo, stepID, channel string) Correlation {
	if correlationID == "" {
		return Correlation{} // unfiltered; resolveCorrelationID has already warned
	}

	locations := info.DeclaredCorrelationLocations()
	if len(locations) == 0 {
		log.Printf("Warning: step %s: the AsyncAPI document declares no correlationId location for channel %q, so the whole message is searched for %q — a message that merely contains that value elsewhere can match; declare 'correlationId.location' on the channel's message to match precisely",
			stepID, channel, correlationID)
		return Correlation{ID: correlationID}
	}

	return Correlation{
		ID:        correlationID,
		Locations: locations,
		// Only needed for a `$message.payload#/…` location on a bytes-only message (a real broker).
		// The transport carries no content type there, so the document's declaration is what decides
		// the format — the same chain the decode below uses, resolved independently because matching
		// happens before a message has been chosen.
		Decode: func(raw []byte) (interface{}, error) {
			serializer, err := se.serializerRegistry().For(info.DeclaredContentType())
			if err != nil {
				return nil, err
			}
			return serializer.Deserialize(raw)
		},
	}
}

// serializerRegistry returns the executor's serializer registry, defaulting to the standard set if a
// StepExecutor was constructed without one (all normal construction goes through NewStepExecutor).
func (se *StepExecutor) serializerRegistry() *SerializerRegistry {
	if se.Serializers != nil {
		return se.Serializers
	}
	// Return the shared default rather than assigning it: writing to se.Serializers here would be a
	// data race the moment two steps run concurrently (Phase 14), and the executor is not otherwise
	// mutated during a run. Built once, lazily, so nothing pays for it when Serializers is configured.
	return defaultSerializers()
}

// defaultSerializers is the fallback registry for a StepExecutor built without one. All normal
// construction goes through NewStepExecutor, which sets Serializers explicitly.
var defaultSerializers = sync.OnceValue(NewDefaultSerializerRegistry)

// previewBytes renders wire bytes for a log line: quoted so the exact characters are visible (a text
// `23.5` and a JSON `"23.5"` are otherwise indistinguishable on screen), and truncated so a large
// message cannot flood the log.
func previewBytes(raw []byte) string {
	const max = 120
	if len(raw) > max {
		return fmt.Sprintf("%q…", raw[:max])
	}
	return fmt.Sprintf("%q", raw)
}

// headerParams pulls the "header" bucket out of the prepared parameters map for use as message headers.
func headerParams(params map[string]interface{}) map[string]interface{} {
	if h, ok := params["header"].(map[string]interface{}); ok {
		return h
	}
	return map[string]interface{}{}
}

// receiveTimeout reads a step's `timeout` (milliseconds) or falls back to a hardcoded default.
func receiveTimeout(step map[string]interface{}) time.Duration {
	if t, ok := toIntValue(step["timeout"]); ok && t > 0 {
		return time.Duration(t) * time.Millisecond
	}
	return 30 * time.Second
}
