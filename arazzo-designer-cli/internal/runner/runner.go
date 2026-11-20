// runner.go replicates the Python arazzo-runner's ArazzoRunner class.
// It is the main entry point for executing Arazzo workflows. It handles
// workflow resolution, dependency execution, step iteration, nested workflows,
// goto/retry actions, and output collection.
package runner

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/wso2/arazzo-designer-cli/internal/evaluator"
	"github.com/wso2/arazzo-designer-cli/internal/failure"
	"github.com/wso2/arazzo-designer-cli/internal/loader"
	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/runner/executor"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// ArazzoRunner executes Arazzo workflows.
type ArazzoRunner struct {
	ArazzoDoc          map[string]interface{}
	SourceDescriptions map[string]interface{}
	Workflows          []interface{}
	RuntimeParams      *models.RuntimeParams
	StepExecutor       *executor.StepExecutor
	Sink               telemetry.SpanEventSink

	// depStack is the active workflow-level dependsOn resolution chain, used to detect circular
	// workflow dependencies (which would otherwise infinite-recurse). It is not used for goto/nested.
	depStack []string
}

// NewArazzoRunner creates a new ArazzoRunner from an Arazzo document path.
func NewArazzoRunner(arazzoFilePath string, runtimeParams *models.RuntimeParams, sink telemetry.SpanEventSink) (*ArazzoRunner, error) {
	// Load the typed Arazzo document (for source description loading)
	typedDoc, err := loader.LoadArazzoDoc(arazzoFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Arazzo document: %w", err)
	}

	// Load the raw Arazzo document (for dynamic evaluation)
	arazzoDoc, err := loader.LoadArazzoDocRaw(arazzoFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load raw Arazzo document: %w", err)
	}

	// Load source descriptions
	sourceDescs, err := loader.LoadSourceDescriptions(typedDoc, arazzoFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load source descriptions: %w", err)
	}

	// Extract workflows
	workflows := toSlice(arazzoDoc["workflows"])
	if len(workflows) == 0 {
		return nil, fmt.Errorf("no workflows found in Arazzo document")
	}

	log.Printf("Loaded Arazzo document with %d workflows", len(workflows))

	// Create step executor
	stepExec := executor.NewStepExecutor(arazzoDoc, sourceDescs, runtimeParams, sink)

	return &ArazzoRunner{
		ArazzoDoc:          arazzoDoc,
		SourceDescriptions: sourceDescs,
		Workflows:          workflows,
		RuntimeParams:      runtimeParams,
		StepExecutor:       stepExec,
		Sink:               sink,
	}, nil
}

// ListWorkflows returns the list of workflow IDs in the Arazzo document.
func (r *ArazzoRunner) ListWorkflows() []string {
	var ids []string
	for _, wfRaw := range r.Workflows {
		wf := toMap(wfRaw)
		if wf == nil {
			continue
		}
		if id, ok := wf["workflowId"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// GetWorkflow finds a workflow by its ID.
func (r *ArazzoRunner) GetWorkflow(workflowID string) map[string]interface{} {
	for _, wfRaw := range r.Workflows {
		wf := toMap(wfRaw)
		if wf == nil {
			continue
		}
		if id, ok := wf["workflowId"].(string); ok && id == workflowID {
			return wf
		}
	}
	return nil
}

// describeSourceDescriptions lists the document's declared sources as {name,url,type}, so a caller
// can see at a glance whether a workflow talks to REST, event-driven, or other Arazzo documents.
func describeSourceDescriptions(doc map[string]interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	for _, raw := range toSlice(doc["sourceDescriptions"]) {
		sd := toMap(raw)
		if sd == nil {
			continue
		}
		entry := map[string]interface{}{"name": sd["name"], "url": sd["url"]}
		if t, _ := sd["type"].(string); t != "" {
			entry["type"] = t
		}
		out = append(out, entry)
	}
	return out
}

// describeStepTarget adds the facts that are NOT written on the step: what kind of step it is, and —
// for an async step — which transport would carry it and what the AsyncAPI document declares for its
// channel.
//
// Everything here resolves through the SAME helpers the runtime uses (ResolveAsyncTarget,
// TransportForSource, AsyncInfo's declarations) rather than re-deriving anything from the step text.
// A description that disagreed with the run would be worse than no description: a step whose
// `operationId` resolves to an AsyncAPI operation is async even though it has no `channelPath`, and
// only asking the resolver gets that right.
func (r *ArazzoRunner) describeStepTarget(step map[string]interface{}, info map[string]interface{}) {
	// The same predicate step_executor dispatches on, character for character - a describer that
	// classified one step differently from the runner would be worse than no describer.
	if wf, _ := step["workflowId"].(string); wf != "" {
		info["stepType"] = "workflow"
		return
	}

	async, isAsync := executor.ResolveAsyncTarget(r.SourceDescriptions, step)
	if !isAsync {
		info["stepType"] = "openapi"
		return
	}
	info["stepType"] = "asyncapi"
	if async == nil {
		return // a channelPath that does not resolve; the step reports it properly when it runs
	}

	if async.ChannelAddress != "" {
		info["channel"] = async.ChannelAddress
	}
	// The operation's action wins over the step's, exactly as resolveAsyncAction decides at runtime.
	if async.Action != "" {
		info["action"] = async.Action
	}

	// Which transport would carry it — or why it cannot run — without building an adapter.
	if transport, err := executor.TransportForSource(toMap(r.SourceDescriptions[async.Source])); err != nil {
		info["adapterError"] = err.Error()
	} else {
		info["adapter"] = transport
	}

	// What the AsyncAPI document declares, so the JSON fallback and the whole-message correlation
	// scan are visible before anything runs.
	if types := async.DeclaredContentTypes(); len(types) > 0 {
		info["contentTypes"] = types
	}
	if locations := async.DeclaredCorrelationLocations(); len(locations) > 0 {
		info["correlationIdLocations"] = locations
	}
}

// GetWorkflowDetails returns metadata about a workflow, including everything v1.1.0 added: the
// document's version/$self/sources, each step's async fields, and the derived facts a caller cannot
// read off the step itself (its type, transport, and what the AsyncAPI document declares).
func (r *ArazzoRunner) GetWorkflowDetails(workflowID string) map[string]interface{} {
	wf := r.GetWorkflow(workflowID)
	if wf == nil {
		return nil
	}

	details := map[string]interface{}{
		"workflowId":  workflowID,
		"summary":     wf["summary"],
		"description": wf["description"],
	}

	// Document-level facts a v1.1.0 description needs. Purely additive: nothing existing changes,
	// and a key is written only when the document actually carries it - so $self appears for v1.1.0
	// only, while arazzoVersion and sourceDescriptions report facts a v1.0.x document always had and
	// simply was never asked for.
	if version, _ := r.ArazzoDoc["arazzo"].(string); version != "" {
		details["arazzoVersion"] = version
	}
	if self, _ := r.ArazzoDoc["$self"].(string); self != "" {
		details["self"] = self
	}
	if sources := describeSourceDescriptions(r.ArazzoDoc); len(sources) > 0 {
		details["sourceDescriptions"] = sources
	}

	// Extract parameters info
	params := toSlice(wf["parameters"])
	var paramList []map[string]interface{}
	for _, pRaw := range params {
		p := toMap(pRaw)
		if p == nil {
			continue
		}
		paramList = append(paramList, map[string]interface{}{
			"name":     p["name"],
			"in":       p["in"],
			"value":    p["value"],
			"required": p["required"],
		})
	}
	details["parameters"] = paramList

	// Extract steps info
	steps := toSlice(wf["steps"])
	var stepList []map[string]interface{}
	for _, sRaw := range steps {
		s := toMap(sRaw)
		if s == nil {
			continue
		}
		info := map[string]interface{}{
			"stepId":        s["stepId"],
			"operationId":   s["operationId"],
			"operationPath": s["operationPath"],
			"workflowId":    s["workflowId"],
			"description":   s["description"],
		}
		// v1.1.0 step fields, passed through only when present so a REST step is unchanged.
		for _, key := range []string{"channelPath", "action", "correlationId", "timeout"} {
			if v, ok := s[key]; ok && v != nil && v != "" {
				info[key] = v
			}
		}
		if deps := toSlice(s["dependsOn"]); len(deps) > 0 {
			info["dependsOn"] = deps
		}
		r.describeStepTarget(s, info)
		stepList = append(stepList, info)
	}
	details["steps"] = stepList

	// Extract dependencies
	dependsOn := toSlice(wf["dependsOn"])
	var depList []string
	for _, d := range dependsOn {
		if ds, ok := d.(string); ok {
			depList = append(depList, ds)
		}
	}
	details["dependsOn"] = depList

	// Extract outputs
	outputs := toMap(wf["outputs"])
	if outputs != nil {
		details["outputs"] = outputs
	}

	return details
}

// ExecuteWorkflow runs a complete workflow from start to finish.
// This is the main entry point for workflow execution. The run gets a trace of its own.
func (r *ArazzoRunner) ExecuteWorkflow(workflowID string, inputs map[string]interface{}) *models.WorkflowExecutionResult {
	return r.executeWorkflowInTrace(workflowID, inputs, nil)
}

// executeWorkflowInTrace runs a workflow, optionally inside a trace that is already open: parent is
// the span this run belongs under - the step that called it - or nil for a top-level run, which
// starts its own trace. One run of a workflow that calls another is one story, so it should read as
// one trace rather than two unrelated ones in a trace viewer.
func (r *ArazzoRunner) executeWorkflowInTrace(workflowID string, inputs map[string]interface{}, parent *telemetry.SpanContext) *models.WorkflowExecutionResult {
	log.Printf("=== Starting workflow execution: %s ===", workflowID)

	// --- Telemetry: workflow start ---
	traceID := telemetry.GenerateTraceID()
	parentSpanID := ""
	if parent != nil {
		// A parent with no span id joins the trace without hanging off anything: a second root, for a
		// run that follows on rather than running inside the caller (a `goto` to another workflow).
		traceID = parent.TraceID
		parentSpanID = parent.SpanID
	}
	workflowSpanID := telemetry.GenerateSpanID()
	workflowStart := time.Now()
	r.Sink.Send(telemetry.TraceEvent{
		Lifecycle:  telemetry.LifecycleStart,
		Context:    telemetry.SpanContext{TraceID: traceID, SpanID: workflowSpanID},
		ParentID:   parentSpanID,
		Name:       workflowID,
		Kind:       telemetry.OTelSpanKindInternal,
		ArazzoKind: telemetry.SpanKindWorkflow,
		StartTime:  workflowStart,
		StatusCode: telemetry.SpanStatusUnset,
		Attributes: map[string]string{
			"workflow.id": workflowID,
		},
	})

	// Helper to emit workflow end span.
	// Pass optional outputs as the last argument to include workflow.inputs and workflow.outputs.
	endWorkflow := func(status telemetry.SpanStatus, errMsg string, workflowOutputs ...map[string]interface{}) {
		dur := float64(time.Since(workflowStart).Milliseconds())
		workflowEnd := time.Now()
		attrs := map[string]string{
			"workflow.id": workflowID,
		}
		if b, err := json.Marshal(inputs); err == nil {
			attrs["workflow.inputs"] = string(b)
		}
		if len(workflowOutputs) > 0 && workflowOutputs[0] != nil {
			if b, err := json.Marshal(workflowOutputs[0]); err == nil {
				attrs["workflow.outputs"] = string(b)
			}
		}
		ev := telemetry.TraceEvent{
			Lifecycle:     telemetry.LifecycleEnd,
			Context:       telemetry.SpanContext{TraceID: traceID, SpanID: workflowSpanID},
			ParentID:      parentSpanID,
			Name:          workflowID,
			Kind:          telemetry.OTelSpanKindInternal,
			ArazzoKind:    telemetry.SpanKindWorkflow,
			StartTime:     workflowStart,
			EndTime:       &workflowEnd,
			DurationMs:    &dur,
			StatusCode:    status,
			StatusMessage: errMsg,
			Attributes:    attrs,
		}
		r.Sink.Send(ev)
	}

	wf := r.GetWorkflow(workflowID)
	if wf == nil {
		endWorkflow(telemetry.SpanStatusError, fmt.Sprintf("Workflow '%s' not found", workflowID))
		return &models.WorkflowExecutionResult{
			Status:     models.WorkflowStatusError,
			WorkflowID: workflowID,
			Error:      fmt.Sprintf("Workflow '%s' not found", workflowID),
			ErrorClass: string(failure.TargetUnresolved),
		}
	}

	// Execute dependencies first
	depOutputs, depStepStatus, err := r.executeDependencies(wf, &telemetry.SpanContext{TraceID: traceID, SpanID: workflowSpanID})
	if err != nil {
		endWorkflow(telemetry.SpanStatusError, fmt.Sprintf("Dependency execution failed: %v", err))
		return &models.WorkflowExecutionResult{
			Status:     models.WorkflowStatusError,
			WorkflowID: workflowID,
			Error:      fmt.Sprintf("Dependency execution failed: %v", err),
			// The dependency's OWN class, carried up. A wrapper class saying only "a dependency
			// failed" would replace the useful answer with a less useful one: what a caller needs
			// to know is that a broker was unreachable, not that the failure happened one level in.
			ErrorClass: string(failure.ClassOf(err)),
		}
	}

	// Merge default parameter values into inputs
	inputs = r.mergeDefaultInputs(wf, inputs)

	// Create execution state
	state := models.NewExecutionState(workflowID, inputs, depOutputs, r.RuntimeParams)
	state.DependencyStepStatus = depStepStatus
	state.TraceID = traceID
	state.WorkflowSpanID = workflowSpanID

	// Populate v1.1.0 runtime-expression context from the Arazzo document so the evaluator can
	// resolve $self, $components.*, $sourceDescriptions.<name>.<field>, and $workflows.<id>.*.
	if self, ok := r.ArazzoDoc["$self"].(string); ok {
		state.Self = self
	}
	state.Components = toMap(r.ArazzoDoc["components"])
	state.SourceDescriptionObjects = buildSourceDescriptionObjects(r.ArazzoDoc["sourceDescriptions"])
	state.WorkflowsByID = buildWorkflowsByID(r.Workflows)

	// Get steps
	steps := toSlice(wf["steps"])
	if len(steps) == 0 {
		endWorkflow(telemetry.SpanStatusError, "Workflow has no steps")
		return &models.WorkflowExecutionResult{
			Status:     models.WorkflowStatusError,
			WorkflowID: workflowID,
			Error:      "Workflow has no steps",
			ErrorClass: string(failure.DocumentInvalid),
		}
	}

	// Start listening on every channel this workflow will receive on BEFORE running step 1. A broker
	// does not replay messages that arrived while nobody was subscribed, so a channel first touched
	// halfway through the run would silently miss anything published before that point.
	r.StepExecutor.PrewarmAsyncChannels(steps)

	// Execute steps sequentially
	stepIndex := 0
	retryCount := map[string]int{}
	maxIterations := len(steps) * 10 // Safety limit to prevent infinite loops
	iterations := 0

	// The first step that failed, with its message and class, kept for the final result below.
	firstFailureStep, firstFailureError, firstFailureClass := "", "", ""

	for stepIndex < len(steps) && iterations < maxIterations {
		iterations++

		stepRaw := steps[stepIndex]
		step := toMap(stepRaw)
		if step == nil {
			stepIndex++
			continue
		}

		stepID, _ := step["stepId"].(string)
		state.CurrentStepID = stepID

		log.Printf("--- Step %d/%d: %s ---", stepIndex+1, len(steps), stepID)

		// v1.1.0: enforce step-level dependsOn as a completion GATE (spec §5.8.5.1). Prerequisites MUST
		// have completed successfully before this step runs; dependsOn does NOT trigger them and does NOT
		// reorder execution. On an unmet prerequisite, hard-fail this step and the workflow.
		if depErr := r.checkStepDependencies(step, state); depErr != nil {
			log.Printf("Step %s blocked by dependsOn: %v", stepID, depErr)
			state.StepsStatus[stepID] = models.StepStatusFailure
			depClass := string(failure.ClassOf(depErr))
			r.StepExecutor.ReportBlockedStep(state, &models.StepResult{StepID: stepID, Error: depErr.Error(), ErrorClass: depClass})
			endWorkflow(telemetry.SpanStatusError, depErr.Error())
			return &models.WorkflowExecutionResult{
				Status:      models.WorkflowStatusError,
				WorkflowID:  workflowID,
				StepOutputs: r.collectStepOutputs(state),
				Inputs:      inputs,
				Error:       depErr.Error(),
				ErrorClass:  depClass,
			}
		}

		// Execute the step
		result := r.StepExecutor.ExecuteStep(step, wf, state)

		// Handle nested workflow
		if result.IsNestedWorkflow && result.NextAction != nil && result.NextAction.WorkflowID != "" {
			nestedResult := r.executeNestedWorkflow(result.NextAction.WorkflowID, step, state, result.PendingSpanID)
			if nestedResult != nil {
				result.Success = nestedResult.Status == models.WorkflowStatusWorkflowComplete
				if !result.Success {
					// A nested workflow that failed must fail the step that called it. Without this
					// the parent reports workflow_complete with no error at all, because a nested
					// step records no status of its own - so a failure one level down disappears
					// entirely from /run and from MCP.
					result.Error = nestedResult.Error
					result.ErrorClass = nestedResult.ErrorClass
					state.StepsStatus[stepID] = models.StepStatusFailure
				}
				// Store nested workflow outputs in state
				if nestedResult.Outputs != nil {
					state.StepsData[stepID] = map[string]interface{}{
						"outputs": nestedResult.Outputs,
					}
				}
				result.NextAction = &models.NextAction{Type: models.ActionTypeContinue}
			}
		}

		// Close the span ExecuteStep left open for a nested workflow call, now that its outcome is
		// known. A no-op for every other step.
		r.StepExecutor.EndNestedWorkflowStep(state, result)

		// Remember the FIRST failure - which step, its message and its class - together, in
		// EXECUTION order. A workflow can run to completion with a failed step (onFailure:
		// continue), and the final result below needs all three to describe ONE step. It sits after
		// the nested-workflow handling above so a nested failure is captured with its own class,
		// and because a nested step arrives here reporting Success=false either way.
		if !result.Success && firstFailureStep == "" {
			firstFailureStep, firstFailureError, firstFailureClass = stepID, result.Error, result.ErrorClass
		}

		// Process the next action
		if result.NextAction != nil {
			switch result.NextAction.Type {
			case models.ActionTypeEnd:
				log.Printf("Workflow ended by action at step %s", stepID)
				status := models.WorkflowStatusWorkflowComplete
				if !result.Success {
					status = models.WorkflowStatusError
				}
				outputs := r.resolveWorkflowOutputs(wf, state)
				if result.Success {
					endWorkflow(telemetry.SpanStatusOK, "", outputs)
				} else {
					// Name the step AND why it failed. "step failed" on its own is what a caller
					// reads in the logs of the step that invoked this workflow, and it says nothing
					// the caller did not already know.
					reason := fmt.Sprintf("step '%s' failed", stepID)
					if result.Error != "" {
						reason = fmt.Sprintf("step '%s' failed: %s", stepID, result.Error)
					}
					endWorkflow(telemetry.SpanStatusError, reason, outputs)
				}
				return &models.WorkflowExecutionResult{
					Status:      status,
					WorkflowID:  workflowID,
					Outputs:     outputs,
					StepOutputs: r.collectStepOutputs(state),
					StepsStatus: state.StepsStatus,
					Inputs:      inputs,
					Error:       result.Error,
					ErrorClass:  result.ErrorClass,
				}

			case models.ActionTypeGoto:
				if result.NextAction.WorkflowID != "" {
					// Goto another workflow
					log.Printf("Goto workflow: %s", result.NextAction.WorkflowID)
					endWorkflow(telemetry.SpanStatusOK, "")
					// Still one run, so still one trace. But this workflow does not wait for the other:
					// it hands over, and `endWorkflow` above already closed its span. So the run below
					// is a second ROOT in the same trace rather than a child that would start after its
					// parent had ended.
					gotoResult := r.executeWorkflowInTrace(result.NextAction.WorkflowID, result.NextAction.Inputs,
						&telemetry.SpanContext{TraceID: traceID})
					return gotoResult
				}
				if result.NextAction.StepID != "" {
					// Goto a specific step
					targetIdx := r.findStepIndex(steps, result.NextAction.StepID)
					if targetIdx >= 0 {
						log.Printf("Goto step: %s (index %d)", result.NextAction.StepID, targetIdx)
						stepIndex = targetIdx
						continue
					}
					log.Printf("Goto target step %s not found", result.NextAction.StepID)
				}

			case models.ActionTypeRetry:
				key := stepID
				if retryCount[key] < result.NextAction.RetryLimit {
					retryCount[key]++
					log.Printf("Retrying step %s (attempt %d/%d)", stepID, retryCount[key], result.NextAction.RetryLimit)
					r.Sink.Send(telemetry.TraceEvent{
						Lifecycle:  telemetry.LifecycleStart,
						Context:    telemetry.SpanContext{TraceID: traceID, SpanID: telemetry.GenerateSpanID()},
						ParentID:   workflowSpanID,
						Name:       stepID,
						Kind:       telemetry.OTelSpanKindInternal,
						ArazzoKind: telemetry.SpanKindRetry,
						StartTime:  time.Now(),
						StatusCode: telemetry.SpanStatusUnset,
						Attributes: map[string]string{
							"step.id":       stepID,
							"retry.attempt": strconv.Itoa(retryCount[key]),
							"retry.limit":   strconv.Itoa(result.NextAction.RetryLimit),
						},
					})
					if result.NextAction.RetryAfter > 0 {
						time.Sleep(time.Duration(result.NextAction.RetryAfter*1000) * time.Millisecond)
					}
					continue // Re-execute current step
				}
				log.Printf("Retry limit reached for step %s, checking remaining failure actions", stepID)

				// After retry exhaustion, re-evaluate onFailure actions skipping retry
				fallbackAction := r.StepExecutor.ActionHandler.HandleFailureAfterRetryExhausted(step, state, stepID)
				if fallbackAction != nil {
					switch fallbackAction.Type {
					case models.ActionTypeEnd:
						log.Printf("Workflow ended by fallback action after retry exhaustion at step %s", stepID)
						outputs := r.resolveWorkflowOutputs(wf, state)
						endWorkflow(telemetry.SpanStatusError, "retry exhausted")
						return &models.WorkflowExecutionResult{
							Status:      models.WorkflowStatusError,
							WorkflowID:  workflowID,
							Outputs:     outputs,
							StepOutputs: r.collectStepOutputs(state),
							Inputs:      inputs,
						}
					case models.ActionTypeGoto:
						if fallbackAction.WorkflowID != "" {
							log.Printf("Fallback goto workflow: %s", fallbackAction.WorkflowID)
							endWorkflow(telemetry.SpanStatusError, "retry exhausted, goto workflow")
							// Same handover as the goto above, after retries ran out: same trace, own root.
							gotoResult := r.executeWorkflowInTrace(fallbackAction.WorkflowID, fallbackAction.Inputs,
								&telemetry.SpanContext{TraceID: traceID})
							return gotoResult
						}
						if fallbackAction.StepID != "" {
							targetIdx := r.findStepIndex(steps, fallbackAction.StepID)
							if targetIdx >= 0 {
								log.Printf("Fallback goto step: %s (index %d)", fallbackAction.StepID, targetIdx)
								stepIndex = targetIdx
								continue
							}
						}
					}
				}

			case models.ActionTypeContinue:
				// Move to next step
			}
		}

		stepIndex++
	}

	if iterations >= maxIterations {
		log.Printf("WARNING: Workflow exceeded maximum iterations (%d)", maxIterations)
	}

	// Resolve workflow outputs
	outputs := r.resolveWorkflowOutputs(wf, state)

	// Determine final status — if any step failed the workflow is an error
	finalStatus := models.WorkflowStatusWorkflowComplete
	for _, ss := range state.StepsStatus {
		if ss == models.StepStatusFailure {
			finalStatus = models.WorkflowStatusError
			break
		}
	}

	// The message and the class must describe the SAME step, so both come from the first failure in
	// execution order, captured together as it happened. Scanning state.StepsStatus for them instead
	// would pick an arbitrary failed step - it is a map, and Go randomises map iteration - so with
	// more than one failure the reported message and class could belong to different steps.
	finalError := ""
	if finalStatus == models.WorkflowStatusError && firstFailureStep != "" {
		finalError = fmt.Sprintf("step '%s' failed", firstFailureStep)
		if firstFailureError != "" {
			finalError = fmt.Sprintf("step '%s' failed: %s", firstFailureStep, firstFailureError)
		}
	}

	log.Printf("=== Workflow %s completed ===", workflowID)
	if finalStatus == models.WorkflowStatusError {
		endWorkflow(telemetry.SpanStatusError, finalError, outputs)
	} else {
		endWorkflow(telemetry.SpanStatusOK, "", outputs)
	}

	return &models.WorkflowExecutionResult{
		Status:      finalStatus,
		WorkflowID:  workflowID,
		Outputs:     outputs,
		StepOutputs: r.collectStepOutputs(state),
		StepsStatus: state.StepsStatus,
		Inputs:      inputs,
		Error:       finalError,
		ErrorClass:  firstFailureClass,
	}
}

// executeDependencies executes all workflows this workflow depends on. Workflow-level dependsOn DOES
// trigger the referenced workflows (spec §5.8.4.1 — unlike step dependsOn), then exposes their outputs
// via $dependencies. A visited chain (depStack) guards against circular workflow dependencies, which
// would otherwise infinite-recurse into a stack overflow.
//
// It returns two maps keyed by dependency workflowId: the workflow outputs (for $dependencies) and the
// per-step status (for the cross-workflow step-level dependsOn gate, so it can check that a specific
// step in a dependency completed successfully — not merely that the workflow ran).
func (r *ArazzoRunner) executeDependencies(wf map[string]interface{}, parent *telemetry.SpanContext) (map[string]map[string]interface{}, map[string]map[string]models.StepStatus, error) {
	depOutputs := make(map[string]map[string]interface{})
	depStepStatus := make(map[string]map[string]models.StepStatus)

	dependsOn := toSlice(wf["dependsOn"])
	if len(dependsOn) == 0 {
		return depOutputs, depStepStatus, nil
	}

	// Cycle guard: record the workflow whose dependencies we're resolving; pop on the way out.
	currentID, _ := wf["workflowId"].(string)
	r.depStack = append(r.depStack, currentID)
	defer func() { r.depStack = r.depStack[:len(r.depStack)-1] }()

	for _, depRaw := range dependsOn {
		depID, ok := depRaw.(string)
		if !ok {
			continue
		}

		// Handle $sourceDescriptions.xxx references (cross-document — execution deferred)
		if strings.HasPrefix(depID, "$sourceDescriptions") {
			// TODO(end-of-project batch): execute cross-document workflow dependsOn once external
			// `type: arazzo` source descriptions are executable. See asyncapi_plan.md "Known Issues".
			log.Printf("Warning: cross-document workflow dependsOn '%s' is not yet executed (skipped)", depID)
			continue
		}

		// Circular dependency: depID is already being resolved higher up the chain.
		for _, active := range r.depStack {
			if active == depID {
				return nil, nil, fmt.Errorf("circular workflow dependsOn detected: %s -> %s", strings.Join(r.depStack, " -> "), depID)
			}
		}

		log.Printf("Executing dependency workflow: %s", depID)
		// A dependency runs inside the span of the workflow that needs it, which is still open and
		// waiting - so it is a child of it, in the same trace. Its failure becomes the caller's
		// failure, and that story is only readable if both are in one place.
		depResult := r.executeWorkflowInTrace(depID, nil, parent)
		if depResult.Status == models.WorkflowStatusError {
			depErr := fmt.Errorf("dependency workflow '%s' failed: %s", depID, depResult.Error)
			if depResult.ErrorClass != "" {
				return nil, nil, failure.Wrap(failure.Class(depResult.ErrorClass), depErr)
			}
			return nil, nil, depErr
		}
		depOutputs[depID] = depResult.Outputs
		depStepStatus[depID] = depResult.StepsStatus
	}

	return depOutputs, depStepStatus, nil
}

// checkStepDependencies enforces the step-level `dependsOn` completion gate (spec §5.8.5.1). It
// returns an error if any prerequisite has not completed SUCCESSFULLY. It never reorders steps and
// never triggers the referenced steps — a synchronous prerequisite is simply required to have
// already run. Reference forms: a local stepId; `$workflows.<wf>.steps.<s>` (cross-workflow, satisfied
// only if that SPECIFIC step reached success in a workflow that ran as a dependency);
// `$sourceDescriptions.<name>.<wf>.steps.<s>` (cross-document — execution deferred, reported as
// unsupported).
func (r *ArazzoRunner) checkStepDependencies(step map[string]interface{}, state *models.ExecutionState) error {
	deps := toSlice(step["dependsOn"])
	if len(deps) == 0 {
		return nil
	}
	stepID, _ := step["stepId"].(string)
	for _, dRaw := range deps {
		dep, _ := dRaw.(string)
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		switch {
		case strings.HasPrefix(dep, "$sourceDescriptions."):
			// TODO(end-of-project batch): support cross-document step dependsOn once external
			// `type: arazzo` source descriptions are executable. See asyncapi_plan.md "Known Issues".
			return failure.Errorf(failure.UnsupportedFeature, "step '%s' dependsOn '%s': cross-document step dependencies are not yet supported", stepID, dep)
		case strings.HasPrefix(dep, "$workflows."):
			wfID, depStepID, ok := parseWorkflowsRef(dep)
			if !ok {
				return failure.Errorf(failure.DocumentInvalid, "step '%s' has a malformed dependsOn reference '%s'", stepID, dep)
			}
			// The workflow must have run as a dependency (its per-step statuses are recorded), AND the
			// specific referenced step must have reached success. Checking the workflow alone is not
			// enough: a step can be skipped (e.g. jumped over by a goto) while the workflow still
			// completes, in which case that step never reached success.
			stepStatuses, ran := state.DependencyStepStatus[wfID]
			if !ran {
				return failure.Errorf(failure.DependencyUnmet, "step '%s' dependsOn '%s', but workflow '%s' has not run as a dependency", stepID, dep, wfID)
			}
			if stepStatuses[depStepID] != models.StepStatusSuccess {
				return failure.Errorf(failure.DependencyUnmet, "step '%s' dependsOn '%s', but step '%s' in workflow '%s' did not complete successfully", stepID, dep, depStepID, wfID)
			}
		default:
			// Local stepId — must have run and reached terminal success.
			if state.StepsStatus[dep] != models.StepStatusSuccess {
				return failure.Errorf(failure.DependencyUnmet, "step '%s' dependsOn '%s', which has not completed successfully", stepID, dep)
			}
		}
	}
	return nil
}

// parseWorkflowsRef splits a "$workflows.<workflowId>.steps.<stepId>" reference into its workflowId
// and stepId. ok is false for anything that isn't that full form (no ".steps." separator, or an empty
// workflowId/stepId), so a malformed reference is rejected by the caller rather than silently
// satisfying the dependsOn gate.
func parseWorkflowsRef(ref string) (workflowID, stepID string, ok bool) {
	rest := strings.TrimPrefix(ref, "$workflows.")
	stepsIdx := strings.Index(rest, ".steps.")
	if stepsIdx <= 0 {
		return "", "", false
	}
	workflowID = rest[:stepsIdx]
	stepID = rest[stepsIdx+len(".steps."):]
	if workflowID == "" || stepID == "" {
		return "", "", false
	}
	return workflowID, stepID, true
}

// buildSourceDescriptionObjects indexes the raw sourceDescriptions list by name, so the evaluator
// can resolve "$sourceDescriptions.<name>.<field>" (e.g. url, type) per spec §5.9.2. Each value is
// the Source Description Object as authored ({name, url, type}).
func buildSourceDescriptionObjects(raw interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for _, sdRaw := range toSlice(raw) {
		sd := toMap(sdRaw)
		if sd == nil {
			continue
		}
		if name, ok := sd["name"].(string); ok && name != "" {
			out[name] = sd
		}
	}
	return out
}

// buildWorkflowsByID indexes workflows by their workflowId so the evaluator can resolve
// "$workflows.<id>.<field>".
func buildWorkflowsByID(workflows []interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for _, wfRaw := range workflows {
		wf := toMap(wfRaw)
		if wf == nil {
			continue
		}
		if id, ok := wf["workflowId"].(string); ok && id != "" {
			out[id] = wf
		}
	}
	return out
}

// mergeDefaultInputs merges workflow-level parameter defaults into inputs.
// Handles both the Arazzo "parameters" array and the "inputs" JSON Schema.
func (r *ArazzoRunner) mergeDefaultInputs(wf map[string]interface{}, inputs map[string]interface{}) map[string]interface{} {
	if inputs == nil {
		inputs = make(map[string]interface{})
	}

	// Handle workflow-level parameters array (Arazzo parameters with name/value)
	params := toSlice(wf["parameters"])
	for _, pRaw := range params {
		p := toMap(pRaw)
		if p == nil {
			continue
		}
		name, _ := p["name"].(string)
		if name == "" {
			continue
		}

		// Only set default if not already provided
		if _, exists := inputs[name]; !exists {
			if val, ok := p["value"]; ok {
				inputs[name] = val
			}
		}
	}

	// Handle workflow-level inputs JSON Schema (Arazzo 1.0.0 style)
	// The "inputs" field is a JSON Schema object with properties that have defaults
	inputsDef := toMap(wf["inputs"])
	if inputsDef != nil {
		properties := toMap(inputsDef["properties"])
		for propName, propDefRaw := range properties {
			propDef := toMap(propDefRaw)
			if propDef == nil {
				continue
			}
			// Only set default if not already provided
			if _, exists := inputs[propName]; !exists {
				if defaultVal, ok := propDef["default"]; ok {
					inputs[propName] = defaultVal
				}
			}
		}
	}

	return inputs
}

// executeNestedWorkflow executes a nested workflow call from a step.
func (r *ArazzoRunner) executeNestedWorkflow(workflowID string, step map[string]interface{}, parentState *models.ExecutionState, callerSpanID string) *models.WorkflowExecutionResult {
	log.Printf("Executing nested workflow: %s", workflowID)

	// Prepare inputs from step parameters
	nestedInputs := make(map[string]interface{})
	params := toSlice(step["parameters"])
	for _, pRaw := range params {
		p := toMap(pRaw)
		if p == nil {
			continue
		}
		name, _ := p["name"].(string)
		value := p["value"]

		// Resolve expressions in parameter values
		if strVal, ok := value.(string); ok && strings.Contains(strVal, "$") {
			resolved := evaluator.EvaluateExpression(strVal, parentState, r.SourceDescriptions, nil)
			if resolved != nil {
				value = resolved
			}
		}
		nestedInputs[name] = value
	}

	// The nested run belongs inside the step that called it, in the caller's trace. ExecuteStep leaves
	// that step's span open for exactly this reason (it is closed once this run is in), so the child's
	// spans sit under a span that is still running. Should the caller's span id ever be missing, fall
	// back to the calling workflow's span: still one trace, just one level flatter.
	parentSpanID := callerSpanID
	if parentSpanID == "" {
		parentSpanID = parentState.WorkflowSpanID
	}
	return r.executeWorkflowInTrace(workflowID, nestedInputs, &telemetry.SpanContext{
		TraceID: parentState.TraceID,
		SpanID:  parentSpanID,
	})
}

// resolveWorkflowOutputs resolves the workflow's output expressions.
func (r *ArazzoRunner) resolveWorkflowOutputs(wf map[string]interface{}, state *models.ExecutionState) map[string]interface{} {
	outputDefs := toMap(wf["outputs"])
	if outputDefs == nil {
		return make(map[string]interface{})
	}

	outputs := make(map[string]interface{})
	for name, exprRaw := range outputDefs {
		exprStr, ok := exprRaw.(string)
		if !ok {
			// v1.1.0 Selector Object → evaluate; anything else is a literal.
			if evaluator.IsSelectorObject(exprRaw) {
				selMap, _ := exprRaw.(map[string]interface{})
				value, err := evaluator.EvaluateSelectorObject(selMap, state, r.SourceDescriptions, nil)
				switch {
				case err == nil && value != nil:
					outputs[name] = value
					log.Printf("Workflow output %s: %v (selector)", name, value)
				case err != nil:
					// Mirror the string-expression path: an unresolved output is "(not available)".
					outputs[name] = "(not available)"
					log.Printf("Workflow output %s: selector failed: %v", name, err)
				default:
					outputs[name] = "(not available)"
					log.Printf("Workflow output %s: selector resolved to nil", name)
				}
			} else {
				outputs[name] = exprRaw
				log.Printf("Workflow output %s: %v (literal)", name, exprRaw)
			}
			continue
		}

		resolved := evaluator.EvaluateExpression(exprStr, state, r.SourceDescriptions, nil)
		if resolved != nil {
			outputs[name] = resolved
			log.Printf("Workflow output %s: %v", name, resolved)
		} else {
			outputs[name] = "(not available)"
			log.Printf("Workflow output %s: unresolved (expression: %s)", name, exprStr)
		}
	}

	return outputs
}

// collectStepOutputs extracts step outputs from the execution state.
func (r *ArazzoRunner) collectStepOutputs(state *models.ExecutionState) map[string]map[string]interface{} {
	stepOutputs := make(map[string]map[string]interface{})
	for stepID, dataRaw := range state.StepsData {
		data := toMap(dataRaw)
		if data == nil {
			continue
		}
		if outputs := toMap(data["outputs"]); outputs != nil {
			stepOutputs[stepID] = outputs
		}
	}
	return stepOutputs
}

// findStepIndex finds the index of a step by its ID.
func (r *ArazzoRunner) findStepIndex(steps []interface{}, stepID string) int {
	for i, sRaw := range steps {
		s := toMap(sRaw)
		if s == nil {
			continue
		}
		if id, ok := s["stepId"].(string); ok && id == stepID {
			return i
		}
	}
	return -1
}

// Helper functions

func toMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func toSlice(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return nil
}
