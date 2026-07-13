// async_executor.go wires AsyncAPI send/receive steps into the runner (Phase 9). It resolves the
// target with the Phase-8 AsyncFinder, then Sends or Receives via the configured Adapter. It reuses
// the existing ParameterProcessor (build payload/headers), SuccessCriteriaChecker, and OutputExtractor
// — feeding them $message instead of an HTTP $response — so async and HTTP steps evaluate criteria and
// outputs through the same code.
package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/evaluator"
	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// resolveAsyncTarget reports whether a step is an AsyncAPI step and, if so, its resolved target.
// A step is async when it has a `channelPath`, or an `operationId` that resolves to an AsyncAPI
// operation (OpenAPI operationIds live under `paths`, not `operations`, so they never match here).
// For a channelPath the step is always treated as async even if resolution fails (nil info), so a
// malformed channel hard-fails rather than silently falling through to the HTTP path.
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
	return nil, false
}

// executeAsyncStep runs a resolved AsyncAPI step (send or receive) against the configured adapter.
func (se *StepExecutor) executeAsyncStep(step map[string]interface{}, info *AsyncInfo, state *models.ExecutionState, stepID string) *models.StepResult {
	if info == nil {
		return se.createFailureResult(stepID, step, state, "AsyncAPI target could not be resolved (channel or operation not found)")
	}
	if se.AsyncAdapter == nil {
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
		return se.executeSend(step, channel, state, stepID)
	case "receive":
		return se.executeReceive(step, channel, state, stepID)
	default:
		return se.createFailureResult(stepID, step, state, fmt.Sprintf("invalid AsyncAPI action %q", action))
	}
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
// it (basic JSON in Phase 9), and publishes it on the channel.
func (se *StepExecutor) executeSend(step map[string]interface{}, channel string, state *models.ExecutionState, stepID string) *models.StepResult {
	var payload interface{}
	if reqBody := toMap(step["requestBody"]); reqBody != nil {
		if prepared := se.ParamProcessor.PrepareRequestBody(reqBody, state); prepared != nil {
			payload = prepared["payload"]
		}
	}
	headers := headerParams(se.ParamProcessor.PrepareParameters(step, state))

	raw, _ := json.Marshal(payload) // basic Phase-9 serialization; Phase 10 formalizes the layer
	msg := &Message{
		Payload:     payload,
		Headers:     headers,
		ContentType: "application/json",
		Raw:         raw,
		Metadata:    map[string]interface{}{},
	}

	if err := se.AsyncAdapter.Send(channel, msg); err != nil {
		return se.createFailureResult(stepID, step, state, fmt.Sprintf("send on channel %q failed: %v", channel, err))
	}

	state.StepsData[stepID] = map[string]interface{}{
		"async": map[string]interface{}{"action": "send", "channel": channel, "payload": payload},
	}
	state.StepsStatus[stepID] = models.StepStatusSuccess
	log.Printf("Step %s: sent a message on channel %q via %s adapter", stepID, channel, se.AsyncAdapter.Name())

	return &models.StepResult{
		StepID:     stepID,
		Success:    true,
		Outputs:    nil,
		NextAction: se.ActionHandler.DetermineNextAction(step, true, state),
	}
}

// executeReceive waits for a (optionally correlated) message on the channel, exposes it as $message,
// then evaluates successCriteria and extracts outputs through the SAME checker/extractor used by HTTP
// steps. A received message with no successCriteria counts as success.
func (se *StepExecutor) executeReceive(step map[string]interface{}, channel string, state *models.ExecutionState, stepID string) *models.StepResult {
	// Correlation id is evaluated against the current context (the incoming message does not exist
	// yet, so a self-referential "$message.*" correlation resolves to nil -> unfiltered/FIFO receive).
	correlationID := ""
	if corrExpr, _ := step["correlationId"].(string); strings.TrimSpace(corrExpr) != "" {
		if v := evaluator.EvaluateExpression(corrExpr, state, se.SourceDescriptions, nil); v != nil {
			correlationID = fmt.Sprintf("%v", v)
		}
	}

	timeout := receiveTimeout(step)
	msg, err := se.AsyncAdapter.Receive(channel, correlationID, timeout)
	if err != nil {
		reason := fmt.Sprintf("receive on channel %q failed: %v", channel, err)
		if errors.Is(err, ErrReceiveTimeout) {
			if correlationID != "" {
				reason = fmt.Sprintf("receive on channel %q timed out after %s: no message matching correlationId %q arrived", channel, timeout, correlationID)
			} else {
				reason = fmt.Sprintf("receive on channel %q timed out after %s: no message arrived", channel, timeout)
			}
		}
		return se.createFailureResult(stepID, step, state, reason)
	}

	// Shape the message for the evaluator's $message root: {header, payload}.
	message := map[string]interface{}{
		"header":  msg.Headers,
		"payload": msg.Payload,
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
	if success {
		state.StepsStatus[stepID] = models.StepStatusSuccess
		log.Printf("Step %s: received a message on channel %q via %s adapter", stepID, channel, se.AsyncAdapter.Name())
	} else {
		state.StepsStatus[stepID] = models.StepStatusFailure
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
		ResponseBody: msg.Payload,
		Outputs:      outputs,
		Error:        failureReason,
		NextAction:   se.ActionHandler.DetermineNextAction(step, success, state),
	}
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
