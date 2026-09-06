package validator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/arazzo/lsp/parser"
	"github.com/arazzo/lsp/utils"
)

// stepSourceName returns the source description a step targets, when the step names one. All three
// targeting fields carry the source in a form the shared helpers already parse, so this asks the
// same question navigation does — "which declared source is this reference pointing at?".
func stepSourceName(step *parser.Step) (string, bool) {
	if step.ChannelPath != "" {
		if name, _, ok := utils.SplitSourceRefAndPointer(step.ChannelPath); ok {
			return name, true
		}
	}
	if step.OperationPath != "" {
		if name, _, ok := utils.SplitSourceRefAndPointer(step.OperationPath); ok {
			return name, true
		}
	}
	if step.OperationID != "" {
		if name, _, ok := utils.ParseScopedOperationID(step.OperationID); ok {
			return name, true
		}
	}
	return "", false
}

// stepMayTargetAsyncAPI reports whether a step could be an AsyncAPI step.
//
// The targeting FIELD does not decide this: a REST step uses `operationId`/`operationPath`, and an
// AsyncAPI step may use any of `operationId`/`operationPath`/`channelPath`. What decides it is the
// SOURCE DESCRIPTION the step points at — its declared `type` is the step's type. So the check is
// "resolve the step's source, then read its type", the same rule the properties panel applies.
//
// A step that names no source (a bare `operationId`) cannot be resolved without loading the source
// files, which the validator does not do. It is then only ruled out when the document declares no
// source that could be AsyncAPI. `type` is optional on a Source Description Object, so an untyped
// source can never be ruled out.
func stepMayTargetAsyncAPI(step *parser.Step, doc *parser.ArazzoDocument) bool {
	if name, named := stepSourceName(step); named {
		sd, found := findSourceDescription(doc, name)
		if !found {
			return true // an unknown source is reported elsewhere; don't pile on a misleading warning
		}
		return sd.Type == "" || sd.Type == "asyncapi"
	}
	for _, sd := range doc.SourceDescriptions {
		if sd.Type == "" || sd.Type == "asyncapi" {
			return true
		}
	}
	return false
}

// findSourceDescription looks up a declared source description by its (already normalized) name.
func findSourceDescription(doc *parser.ArazzoDocument, name string) (parser.SourceDescription, bool) {
	for _, sd := range doc.SourceDescriptions {
		if sd.Name == name {
			return sd, true
		}
	}
	return parser.SourceDescription{}, false
}

// componentKeyRegex matches valid component key names per Arazzo spec §5.8.9
var componentKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9\.\-_]+$`)

// ValidationError represents a validation error
type ValidationError struct {
	Line     int
	Column   int
	Message  string
	Severity string // "error" or "warning"
}

// Validator validates Arazzo documents
type Validator struct {
	parser *parser.Parser

	// resolveStepAction, when set, returns the action ("send"/"receive") declared by the AsyncAPI
	// operation a step targets, resolved from the indexed source documents.
	//
	// The validator itself only ever sees the Arazzo text, so on its own it can classify a step as a
	// send or a receive ONLY when the step writes `action:` explicitly. With `operationId`/
	// `operationPath` the direction lives in the AsyncAPI document — which is exactly the form the
	// spec prefers. This hook lets a caller that already has the operation index (the LSP server)
	// supply that missing fact, without the validator gaining file access.
	//
	// nil in content-only contexts (unit tests, any caller without an index); the checks that need it
	// simply stay quiet rather than guessing.
	resolveStepAction func(step *parser.Step) (action string, ok bool)

	// resolveStepContentType, when set, returns every DISTINCT content type the AsyncAPI document
	// declares for the channel a step targets, and whether that channel was resolved at all. Same
	// arrangement as resolveStepAction: the fact lives in another document, so a caller holding the
	// index supplies it.
	//
	// resolved=false means "could not be established" and every content-type check stays quiet.
	// resolved=true with an EMPTY slice is meaningful — the channel exists and declares no format, so
	// the runtime falls back to JSON. More than one entry is also meaningful: the channel carries
	// messages of different formats and the document cannot say which one a step sends.
	resolveStepContentType func(step *parser.Step) (declared []string, resolved bool)

	// resolveStepCorrelationLocation reports where the AsyncAPI document says the correlation id lives
	// for the channel a step targets, and the name of the source description that document was declared
	// under (so the advice can point at the file the author must edit).
	//
	// resolved=false means "could not be established" and the check stays quiet. resolved=true with an
	// EMPTY slice is the meaningful case: the channel exists and declares no location, so a receive has
	// to search the whole message and can match the wrong one.
	resolveStepCorrelationLocation func(step *parser.Step) (locations []string, source string, resolved bool)

	// resolveSourceDeclaresServers reports whether an AsyncAPI source description's document declares a
	// `servers` section. That section is what picks a transport; without it every channel in the file
	// runs on the in-memory adapter and no broker is contacted.
	//
	// resolved=false means the source could not be read (unresolvable path, not indexed yet) and the
	// check stays quiet rather than claiming a document declares nothing.
	resolveSourceDeclaresServers func(sd *parser.SourceDescription) (declaresServers bool, resolved bool)
}

// NewValidator creates a new Validator
func NewValidator() *Validator {
	return &Validator{
		parser: parser.NewParser(),
	}
}

// WithStepActionResolver returns the validator configured to resolve a step's AsyncAPI action
// through fn. Passing nil restores content-only behaviour.
func (v *Validator) WithStepActionResolver(fn func(step *parser.Step) (string, bool)) *Validator {
	v.resolveStepAction = fn
	return v
}

// WithStepContentTypeResolver returns the validator configured to resolve the content type an
// AsyncAPI document declares for a step's channel through fn. Passing nil restores content-only
// behaviour.
func (v *Validator) WithStepContentTypeResolver(fn func(step *parser.Step) ([]string, bool)) *Validator {
	v.resolveStepContentType = fn
	return v
}

// WithStepCorrelationLocationResolver returns the validator configured to resolve where the AsyncAPI
// document says a step's correlation id lives, through fn. Passing nil restores content-only behaviour.
func (v *Validator) WithStepCorrelationLocationResolver(fn func(step *parser.Step) ([]string, string, bool)) *Validator {
	v.resolveStepCorrelationLocation = fn
	return v
}

// WithSourceAdapterResolver returns the validator configured to resolve whether an AsyncAPI source
// declares `servers` through fn. Passing nil restores content-only behaviour.
func (v *Validator) WithSourceAdapterResolver(fn func(sd *parser.SourceDescription) (bool, bool)) *Validator {
	v.resolveSourceDeclaresServers = fn
	return v
}

// stepAction returns the direction an async step will actually run with.
//
// The AsyncAPI operation's action comes FIRST, because that is what the runtime does: when a step
// targets an operation, the operation's declared action wins and a contradicting step `action` is only
// warned about (spec/Phase-8 decision, enforced in resolveAsyncAction). Trusting the step's own
// `action` here would make every direction-dependent check reason about a direction the run will not
// take — a step declaring `receive` on a send operation really sends, so the send-only checks must
// fire on it and the receive-only ones must not.
//
// The step's `action` is the answer when no operation action is available: a `channelPath` step has no
// operation, and the resolver is nil in content-only contexts. ok is false when neither is available,
// in which case direction-dependent checks must stay quiet rather than guess.
func (v *Validator) stepAction(step *parser.Step) (string, bool) {
	if v.resolveStepAction != nil {
		if action, ok := v.resolveStepAction(step); ok && action != "" {
			return action, true
		}
	}
	if step.Action != "" {
		return step.Action, true
	}
	return "", false
}

// Validate validates an Arazzo document and returns validation errors
func (v *Validator) Validate(doc *parser.ArazzoDocument) []ValidationError {
	errors := []ValidationError{}

	// Validate document-level fields
	errors = append(errors, v.validateDocumentLevel(doc)...)

	// Validate $self URI-reference (v1.1.0 spec §5.8.1.1)
	errors = append(errors, v.validateSelf(doc)...)

	// Validate component key naming rules (v1.1.0 spec §5.8.9)
	errors = append(errors, v.validateComponentKeys(doc)...)

	// Validate source descriptions
	errors = append(errors, v.validateSourceDescriptions(doc)...)
	errors = append(errors, v.validateSourceAdapters(doc)...)

	// Validate workflows
	errors = append(errors, v.validateWorkflows(doc)...)

	return errors
}

// validateDocumentLevel validates top-level document fields
func (v *Validator) validateDocumentLevel(doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError

	// Validate arazzo version
	if doc.Arazzo == "" {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  "Missing required field 'arazzo'",
			Severity: "error",
		})
	} else if doc.Arazzo != "1.0.0" && doc.Arazzo != "1.0.1" && doc.Arazzo != "1.1.0" {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  fmt.Sprintf("Invalid arazzo version: %s (expected 1.0.0, 1.0.1, or 1.1.0)", doc.Arazzo),
			Severity: "error",
		})
	}

	// Validate info
	if doc.Info.Title == "" {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  "Missing required field 'info.title'",
			Severity: "error",
		})
	}
	if doc.Info.Version == "" {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  "Missing required field 'info.version'",
			Severity: "error",
		})
	}

	// Validate sourceDescriptions
	if len(doc.SourceDescriptions) == 0 {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  "Missing required field 'sourceDescriptions' (must have at least one)",
			Severity: "error",
		})
	}

	// Validate workflows
	if len(doc.Workflows) == 0 {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  "Missing required field 'workflows' (must have at least one)",
			Severity: "error",
		})
	}

	return errors
}

// validateSourceDescriptions validates source descriptions
func (v *Validator) validateSourceDescriptions(doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError

	for i, sd := range doc.SourceDescriptions {
		if sd.Name == "" {
			errors = append(errors, ValidationError{
				Line:     0,
				Column:   0,
				Message:  fmt.Sprintf("sourceDescriptions[%d]: Missing required field 'name'", i),
				Severity: "error",
			})
		}
		if sd.URL == "" {
			errors = append(errors, ValidationError{
				Line:     0,
				Column:   0,
				Message:  fmt.Sprintf("sourceDescriptions[%d]: Missing required field 'url'", i),
				Severity: "error",
			})
		}
		if sd.Type != "" && sd.Type != "openapi" && sd.Type != "asyncapi" && sd.Type != "arazzo" {
			errors = append(errors, ValidationError{
				Line:     0,
				Column:   0,
				Message:  fmt.Sprintf("sourceDescriptions[%d]: Invalid type '%s' (must be 'openapi', 'asyncapi', or 'arazzo')", i, sd.Type),
				Severity: "error",
			})
		}
	}

	return errors
}

// validateWorkflows validates all workflows
func (v *Validator) validateWorkflows(doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError

	// Detect circular workflow-level dependsOn across the document — spec §5.8.4.1 (workflow dependsOn
	// TRIGGERS the referenced workflows, so a cycle would infinite-recurse at runtime).
	errors = append(errors, v.validateWorkflowDependsOnCycles(doc)...)

	workflowIDs := make(map[string]bool)

	for _, workflow := range doc.Workflows {
		// Check for duplicate workflowId
		if workflowIDs[workflow.WorkflowID] {
			errors = append(errors, ValidationError{
				Line:     workflow.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Duplicate workflowId: %s", workflow.WorkflowID),
				Severity: "error",
			})
		}
		workflowIDs[workflow.WorkflowID] = true

		// Validate required fields
		if workflow.WorkflowID == "" {
			errors = append(errors, ValidationError{
				Line:     workflow.LineNumber,
				Column:   0,
				Message:  "Missing required field 'workflowId'",
				Severity: "error",
			})
		}

		if len(workflow.Steps) == 0 {
			errors = append(errors, ValidationError{
				Line:     workflow.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Workflow '%s': Missing required field 'steps' (must have at least one)", workflow.WorkflowID),
				Severity: "error",
			})
		}

		// Validate steps
		errors = append(errors, v.validateSteps(&workflow, doc)...)
	}

	return errors
}

// validateSteps validates all steps in a workflow
func (v *Validator) validateSteps(workflow *parser.Workflow, doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError

	// Detect local (same-workflow) dependsOn cycles up front — spec §5.8.5.1 requires a resolvable order.
	errors = append(errors, v.validateDependsOnCycles(workflow)...)

	stepIDs := make(map[string]bool)

	for _, step := range workflow.Steps {
		// Check for duplicate stepId
		if stepIDs[step.StepID] {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Duplicate stepId: %s", step.StepID),
				Severity: "error",
			})
		}
		stepIDs[step.StepID] = true

		// Validate required fields
		if step.StepID == "" {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  "Missing required field 'stepId'",
				Severity: "error",
			})
		}

		// Validate that step has exactly one of: operationId, operationPath, channelPath, or workflowId
		actionCount := 0
		if step.OperationID != "" {
			actionCount++
		}
		if step.OperationPath != "" {
			actionCount++
		}
		if step.ChannelPath != "" {
			actionCount++
		}
		if step.WorkflowID != "" {
			actionCount++
		}

		if actionCount == 0 {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': Must have one of 'operationId', 'operationPath', 'channelPath', or 'workflowId'", step.StepID),
				Severity: "error",
			})
		} else if actionCount > 1 {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': Can only have one of 'operationId', 'operationPath', 'channelPath', or 'workflowId'", step.StepID),
				Severity: "error",
			})
		}

		// Validate action enum: if set, must be "send" or "receive" (spec §5.8.5)
		if step.Action != "" && step.Action != "send" && step.Action != "receive" {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': Invalid action '%s' (must be 'send' or 'receive')", step.StepID, step.Action),
				Severity: "error",
			})
		}

		// 'action' is only applicable to AsyncAPI steps (spec §5.8.5: "Only applicable for asyncapi steps").
		// An AsyncAPI step is a `channelPath` step OR an `operationId` step that targets an AsyncAPI
		// operation, so this must not be tied to `channelPath` alone.
		if step.Action != "" && !stepMayTargetAsyncAPI(&step, doc) {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': 'action' is only applicable to AsyncAPI steps (set 'channelPath', or an 'operationId'/'operationPath' that targets an AsyncAPI source)", step.StepID),
				Severity: "warning",
			})
		}

		// A 'channelPath' step needs an 'action': a channel has no direction (AsyncAPI 3.x puts
		// direction on operations), so without 'action' the send/receive intent is undefined and the
		// runtime cannot proceed.
		if step.ChannelPath != "" && step.Action == "" {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': a 'channelPath' step must also specify 'action' ('send' or 'receive') — the message-flow direction is otherwise undefined", step.StepID),
				Severity: "error",
			})
		}

		// TODO(phase8/9): flag an operationId/action MISMATCH (step 'action' contradicts the referenced
		// AsyncAPI operation's action). Deferred here because it needs cross-source resolution (loading
		// the AsyncAPI doc + finding the operation), which this validator doesn't do yet. The CLI
		// resolver (AsyncFinder.ActionMismatch) already detects it; runtime enforces (doc wins + warn).

		// Validate the step's target reference (spec §5.8.5). `channelPath` and `operationPath` are
		// both "<sourceRef>#<jsonPointer>"; the scoped `operationId` form is
		// "$sourceDescriptions.<name>.<operationId>". In every case the source may be written as a
		// bare name OR as the spec's runtime-expression form ("{$sourceDescriptions.<name>.url}") —
		// utils.NormalizeSourceRef reduces both to the same name, so validation can't disagree with
		// navigation about which source a step points at.
		if step.ChannelPath != "" {
			sdName, _, ok := utils.SplitSourceRefAndPointer(step.ChannelPath)
			if !ok {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'channelPath' must be in the format '<sourceDescription>#<jsonPointer>' — the source may be a name or '{$sourceDescriptions.<name>.url}' (got '%s')", step.StepID, step.ChannelPath),
					Severity: "error",
				})
			} else if sd, found := findSourceDescription(doc, sdName); !found {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'channelPath' references unknown source description '%s'", step.StepID, sdName),
					Severity: "warning",
				})
			} else if sd.Type != "" && sd.Type != "asyncapi" {
				// `type` is optional on a Source Description Object, so only a type that is present
				// AND contradicts the reference is an error (matching the operationPath check below).
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'channelPath' references source '%s' which has type '%s', but must be 'asyncapi'", step.StepID, sdName, sd.Type),
					Severity: "error",
				})
			}
		}

		// `operationPath` targets an operation by JSON Pointer. It may point into an OpenAPI document
		// (#/paths/~1pets/get) or an AsyncAPI one (#/operations/placeOrder) — the spec words it as
		// "an operation" without restricting the document type — but never into an `arazzo` source,
		// which describes workflows rather than operations.
		if step.OperationPath != "" {
			sdName, _, ok := utils.SplitSourceRefAndPointer(step.OperationPath)
			if !ok {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'operationPath' must be in the format '<sourceDescription>#<jsonPointer>' — the source may be a name or '{$sourceDescriptions.<name>.url}' (got '%s')", step.StepID, step.OperationPath),
					Severity: "error",
				})
			} else if sd, found := findSourceDescription(doc, sdName); !found {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'operationPath' references unknown source description '%s'", step.StepID, sdName),
					Severity: "warning",
				})
			} else if sd.Type == "arazzo" {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'operationPath' references source '%s' which has type 'arazzo' — use 'workflowId' to target an Arazzo workflow", step.StepID, sdName),
					Severity: "error",
				})
			}
		}

		// A scoped `operationId` ("$sourceDescriptions.<name>.<operationId>") must name a declared
		// source too. A bare operationId is resolved across sources at runtime, so there is nothing
		// to check here.
		if step.OperationID != "" {
			if sdName, _, scoped := utils.ParseScopedOperationID(step.OperationID); scoped {
				if _, found := findSourceDescription(doc, sdName); !found {
					errors = append(errors, ValidationError{
						Line:     step.LineNumber,
						Column:   0,
						Message:  fmt.Sprintf("Step '%s': 'operationId' references unknown source description '%s'", step.StepID, sdName),
						Severity: "warning",
					})
				}
			} else if strings.HasPrefix(strings.TrimSpace(step.OperationID), "$") {
				// A '$' prefix that isn't the scoped form is a malformed runtime expression.
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'operationId' expression must be '$sourceDescriptions.<name>.<operationId>' (got '%s')", step.StepID, step.OperationID),
					Severity: "error",
				})
			}
		}

		// Validate timeout is non-negative (spec §5.8.5)
		if step.Timeout < 0 {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': 'timeout' must be a non-negative integer (milliseconds), got %d", step.StepID, step.Timeout),
				Severity: "error",
			})
		}

		// Validate dependsOn references (spec §5.8.5)
		errors = append(errors, v.validateDependsOn(&step, workflow, doc)...)

		// The AsyncAPI operation a step targets declares its own direction. When the step ALSO writes
		// `action`, the two must agree — at runtime the operation wins and the step's action is
		// ignored, so a contradiction means the workflow does the opposite of what it says.
		if step.Action != "" && v.resolveStepAction != nil {
			if opAction, ok := v.resolveStepAction(&step); ok && opAction != "" && opAction != step.Action {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'action' is '%s' but the referenced AsyncAPI operation declares '%s' — at runtime the operation wins, so this step will %s", step.StepID, step.Action, opAction, opAction),
					Severity: "warning",
				})
			}
		}

		// A receive with no 'correlationId' consumes whatever message is next on the channel. That is
		// legal and often intended, but on a shared channel it can pick up a message this workflow
		// never sent — so it is worth surfacing while authoring, not only in the run log.
		//
		// The direction comes from the step's `action` when written, otherwise from the AsyncAPI
		// operation it targets; if neither is available the check stays quiet rather than guessing.
		if action, known := v.stepAction(&step); known && action == "receive" && step.CorrelationID == "" {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': no 'correlationId' — this receive will consume the next message on the channel without filtering, which may be a message this workflow did not send", step.StepID),
				Severity: "warning",
			})
		}

		// 'correlationId' is only applicable to AsyncAPI steps with action 'receive' (spec §5.8.5).
		// As with 'action', whether a step is AsyncAPI depends on the source it targets, not on
		// which targeting field it used.
		if step.CorrelationID != "" {
			if !stepMayTargetAsyncAPI(&step, doc) {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'correlationId' is only meaningful on AsyncAPI steps (set 'channelPath', or an 'operationId'/'operationPath' that targets an AsyncAPI source)", step.StepID),
					Severity: "warning",
				})
			} else if action, known := v.stepAction(&step); known && action != "receive" {
				// The direction is the step's own `action` when written, otherwise the one resolved
				// from the AsyncAPI operation — so a send carrying a correlationId is caught in both
				// forms. When neither is available the check stays quiet.
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'correlationId' is only applicable to AsyncAPI steps with action 'receive'", step.StepID),
					Severity: "warning",
				})
			}
		}

		// Content type of the message a send step publishes (spec §5.8.14.1).
		errors = append(errors, v.validateMessageContentType(&step)...)
		errors = append(errors, v.validateCorrelationLocation(&step)...)

		// Validate parameter 'in' locations (spec §5.8.6)
		errors = append(errors, v.validateParameterLocations(step.Parameters, step.StepID, step.LineNumber)...)

		// Validate reusable-object references on step parameters resolve to a parameter component
		for _, p := range step.Parameters {
			errors = append(errors, v.validateComponentReference(p.Reference, doc, "parameters", step.StepID, step.LineNumber)...)
		}

		// Validate successCriteria is non-empty when the key is present (spec §5.8.5.1)
		if step.SuccessCriteria != nil && len(step.SuccessCriteria) == 0 {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': 'successCriteria' is defined but empty; when present it must contain at least one Criterion Object", step.StepID),
				Severity: "error",
			})
		}

		// Validate Expression Type Objects used as a criterion 'type' (spec §5.8.12)
		for ci, crit := range step.SuccessCriteria {
			errors = append(errors, v.validateExpressionType(crit.Type, fmt.Sprintf("Step '%s' successCriteria[%d]", step.StepID, ci), step.LineNumber)...)
		}

		// Validate onSuccess actions (spec §5.8.7): type enum {goto,end}, target exclusivity,
		// reusable references, and parameter rules (§5.8.7.1).
		for i, action := range step.OnSuccess {
			ref := fmt.Sprintf("onSuccess[%d]", i)
			errors = append(errors, v.validateComponentReference(action.Reference, doc, "successActions", step.StepID, step.LineNumber)...)
			if action.Reference == "" {
				errors = append(errors, v.validateActionType(action.Type, []string{"goto", "end"}, ref, step.StepID, step.LineNumber)...)
				errors = append(errors, v.validateActionTarget(action.StepID, action.WorkflowID, ref, step.StepID, step.LineNumber)...)
			}
			errors = append(errors, v.validateActionParameters(action.Parameters, action.WorkflowID, "Arazzo spec section 5.8.7.1", ref, step.StepID, step.LineNumber)...)
		}

		// Validate onFailure actions (spec §5.8.8): type enum {goto,end,retry}, target exclusivity,
		// reusable references, and parameter rules (§5.8.8.1).
		for i, action := range step.OnFailure {
			ref := fmt.Sprintf("onFailure[%d]", i)
			errors = append(errors, v.validateComponentReference(action.Reference, doc, "failureActions", step.StepID, step.LineNumber)...)
			if action.Reference == "" {
				errors = append(errors, v.validateActionType(action.Type, []string{"goto", "end", "retry"}, ref, step.StepID, step.LineNumber)...)
				errors = append(errors, v.validateActionTarget(action.StepID, action.WorkflowID, ref, step.StepID, step.LineNumber)...)
			}
			errors = append(errors, v.validateActionParameters(action.Parameters, action.WorkflowID, "Arazzo spec section 5.8.8.1", ref, step.StepID, step.LineNumber)...)
		}

		// Validate runtime expressions
		errors = append(errors, v.validateRuntimeExpressions(&step, workflow, doc)...)
	}

	return errors
}

// validateRuntimeExpressions validates runtime expressions in parameters and values
func (v *Validator) validateRuntimeExpressions(step *parser.Step, workflow *parser.Workflow, doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError

	// Regular expression to match runtime expressions
	runtimeExprRegex := regexp.MustCompile(`\$\{?(\w+)\.([^}]+)\}?`)

	// Validate parameters
	for _, param := range step.Parameters {
		if valueStr, ok := param.Value.(string); ok {
			matches := runtimeExprRegex.FindAllStringSubmatch(valueStr, -1)
			for _, match := range matches {
				if len(match) > 1 {
					prefix := match[1]    // e.g., "steps", "inputs", "workflows"
					reference := match[2] // e.g., "step-1.outputs.id"

					switch prefix {
					case "steps":
						// Extract stepId from reference
						parts := strings.SplitN(reference, ".", 2)
						if len(parts) > 0 {
							refStepID := parts[0]
							// A reference to a step that does not exist is always wrong. A reference to
							// a step declared LATER is normally wrong too — but not always: a `goto`
							// can send execution backwards, so a later step may well have already run.
							// Declaration order is not execution order, so that case is a warning.
							if !v.stepExistsInWorkflow(workflow, refStepID) {
								errors = append(errors, ValidationError{
									Line:     step.LineNumber,
									Column:   0,
									Message:  fmt.Sprintf("Step '%s': Referenced step '%s' does not exist in this workflow", step.StepID, refStepID),
									Severity: "error",
								})
							} else if !v.stepExistsBeforeCurrent(workflow, refStepID, step.StepID) {
								errors = append(errors, ValidationError{
									Line:     step.LineNumber,
									Column:   0,
									Message:  fmt.Sprintf("Step '%s': Referenced step '%s' is declared after this step, so its outputs are unavailable unless a 'goto' runs it first", step.StepID, refStepID),
									Severity: "warning",
								})
							}
						}
					case "workflows":
						// Extract workflowId from reference
						parts := strings.SplitN(reference, ".", 2)
						if len(parts) > 0 {
							refWorkflowID := parts[0]
							// Check if referenced workflow exists
							if v.parser.FindWorkflowByID(doc, refWorkflowID) == nil {
								errors = append(errors, ValidationError{
									Line:     step.LineNumber,
									Column:   0,
									Message:  fmt.Sprintf("Step '%s': Referenced workflow '%s' does not exist", step.StepID, refWorkflowID),
									Severity: "error",
								})
							}
						}
					}
				}
			}
		}
	}

	return errors
}

// stepExistsBeforeCurrent checks if a step exists before the current step
// stepExistsInWorkflow reports whether a workflow declares a step with this id, regardless of order.
func (v *Validator) stepExistsInWorkflow(workflow *parser.Workflow, targetStepID string) bool {
	for _, step := range workflow.Steps {
		if step.StepID == targetStepID {
			return true
		}
	}
	return false
}

func (v *Validator) stepExistsBeforeCurrent(workflow *parser.Workflow, targetStepID, currentStepID string) bool {
	for _, step := range workflow.Steps {
		if step.StepID == currentStepID {
			return false // Reached current step without finding target
		}
		if step.StepID == targetStepID {
			return true // Found target before current
		}
	}
	return false
}

// validateSelf validates the $self field (spec §5.8.1.1).
// $self MUST be a URI-reference without a fragment identifier.
func (v *Validator) validateSelf(doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError
	if doc.Self == "" {
		return errors
	}
	if strings.Contains(doc.Self, "#") {
		errors = append(errors, ValidationError{
			Line:     0,
			Column:   0,
			Message:  "The '$self' field MUST NOT contain a fragment identifier (#) (Arazzo spec section 5.8.1.1)",
			Severity: "error",
		})
	}
	return errors
}

// validateComponentKeys validates that all component map keys match the required naming pattern (spec §5.8.9).
// Valid keys must match: ^[a-zA-Z0-9\.\-_]+$
func (v *Validator) validateComponentKeys(doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError
	if doc.Components == nil {
		return errors
	}
	check := func(section, key string) {
		if !componentKeyRegex.MatchString(key) {
			errors = append(errors, ValidationError{
				Line:     0,
				Column:   0,
				Message:  fmt.Sprintf("components.%s key '%s' contains invalid characters (must match [a-zA-Z0-9.\\-_]+)", section, key),
				Severity: "error",
			})
		}
	}
	for key := range doc.Components.Inputs {
		check("inputs", key)
	}
	for key := range doc.Components.Parameters {
		check("parameters", key)
	}
	for key := range doc.Components.SuccessActions {
		check("successActions", key)
	}
	for key := range doc.Components.FailureActions {
		check("failureActions", key)
	}
	return errors
}

// validateMessageContentType surfaces which serializer a step's message will actually go through.
//
// Arazzo §5.8.14.1 on a Request Body Object's contentType: "The Content-Type for the request content.
// If omitted then refer to Content-Type specified at the targeted operation to understand
// serialization requirements." So the step's own value is authoritative and the AsyncAPI document is
// consulted only in its absence.
//
// The same three questions are asked in BOTH directions, because both end up choosing a serializer:
//
//  1. Neither declares one. Legal, and the runtime falls back to JSON — but that is an assumption
//     nothing in either document states, and it is wrong for a channel that really carries text.
//     Reported as information: the document is correct, it is just silent.
//  2. The document declares MORE THAN ONE. Nothing then says which one this message is, so the runtime
//     picks the first deterministically. That is a guess, and worth surfacing before it runs.
//  3. (send only) Both declare one and they disagree. The step wins, so the message goes out in a
//     format the AsyncAPI document — the contract every other consumer reads — does not describe.
//
// What differs is only the ADVICE. A send can settle it on the step; a receive has no requestBody, so
// its only fix is in the AsyncAPI document. Everything depends on resolving the target: with no
// resolver, or a channel that cannot be reached, every check stays quiet.
func (v *Validator) validateMessageContentType(step *parser.Step) []ValidationError {
	if v.resolveStepContentType == nil {
		return nil
	}
	action, known := v.stepAction(step)
	if !known || (action != "send" && action != "receive") {
		return nil
	}
	sending := action == "send"

	// Only a send can carry one: a receive step has no requestBody.
	stepContentType := ""
	if sending && step.RequestBody != nil {
		stepContentType = strings.TrimSpace(step.RequestBody.ContentType)
	}

	declared, resolved := v.resolveStepContentType(step)
	if !resolved {
		return nil
	}

	if stepContentType == "" {
		switch {
		case len(declared) == 0:
			message := fmt.Sprintf("Step '%s': the AsyncAPI document declares no 'contentType' for this channel — an incoming message that carries none itself will be decoded as 'application/json'", step.StepID)
			if sending {
				message = fmt.Sprintf("Step '%s': no 'contentType' on the requestBody and the AsyncAPI document declares none for this channel — the message will be serialized as 'application/json'", step.StepID)
			}
			return []ValidationError{{
				Line:     step.LineNumber,
				Column:   0,
				Message:  message,
				Severity: "information",
			}}
		case len(declared) > 1:
			message := fmt.Sprintf("Step '%s': the AsyncAPI document declares more than one contentType for this channel (%s) — an incoming message that carries none itself will be decoded as '%s'; declare one format per channel so the decoder is unambiguous", step.StepID, strings.Join(declared, ", "), declared[0])
			if sending {
				message = fmt.Sprintf("Step '%s': the AsyncAPI document declares more than one contentType for this channel (%s) — '%s' will be used; set 'contentType' on the requestBody to choose explicitly", step.StepID, strings.Join(declared, ", "), declared[0])
			}
			return []ValidationError{{
				Line:     step.LineNumber,
				Column:   0,
				Message:  message,
				Severity: "warning",
			}}
		}
		return nil // exactly one declared: the document answers the question
	}

	// A disagreement is only a disagreement when the step's format is NOT one the document declares.
	// On a channel carrying several formats, naming the second one is correct, not a contradiction.
	if len(declared) > 0 && !containsMediaType(declared, stepContentType) {
		return []ValidationError{{
			Line:     step.LineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': requestBody 'contentType' is '%s' but the AsyncAPI document declares '%s' for this channel — the value declared in this step overrides the AsyncAPI declaration, so this message will not match the format the document describes", step.StepID, stepContentType, strings.Join(declared, ", ")),
			Severity: "warning",
		}}
	}
	return nil
}

// validateSourceAdapters points out that an AsyncAPI source with no `servers` runs in-memory.
//
// Worth saying because of HOW that mode fails: an in-memory send/receive pair ALWAYS succeeds, since
// the workflow receives the message it just sent. So a document that simply forgot `servers` produces a
// completely green run that never contacted a broker — a passing workflow that proves nothing.
//
// Reported as `information`, not a warning: running in-memory is a supported, deliberate mode for
// tests and local runs (it is what every Phase 9/10 example uses), so it is a default worth stating,
// not a mistake. One marker per source rather than per step, so a document full of async steps does
// not fill with markers saying the same thing.
func (v *Validator) validateSourceAdapters(doc *parser.ArazzoDocument) []ValidationError {
	if v.resolveSourceDeclaresServers == nil {
		return nil
	}
	var errors []ValidationError
	for i := range doc.SourceDescriptions {
		sd := &doc.SourceDescriptions[i]
		if sd.Type != "asyncapi" {
			continue
		}
		declaresServers, resolved := v.resolveSourceDeclaresServers(sd)
		if !resolved || declaresServers {
			continue
		}
		errors = append(errors, ValidationError{
			Line:     sd.LineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Source description '%s': the AsyncAPI document declares no 'servers', so steps targeting it run on the default in-memory adapter — no broker is contacted, and a receive can only ever see messages this workflow itself sent. Declare 'servers' in the AsyncAPI document to run against a real broker.", sd.Name),
			Severity: "information",
		})
	}
	return errors
}

// validateCorrelationLocation surfaces HOW a receive step will match its correlation id.
//
// AsyncAPI lets a message declare where its id lives (`correlationId.location`), and when it does the
// runtime reads exactly there. When it does not, the runtime has to search the entire message — every
// header, every scalar in the payload, and for a message that arrived as bytes (any real broker) the
// raw body as a substring. That scan matches a message which merely happens to carry the same value
// somewhere else, so the workflow proceeds on the wrong message and reports success.
//
// The author cannot see any of that from the Arazzo file, which is why it is worth saying out loud.
// Reported as `information`, not a warning: nothing here is wrong or unsupported, and the fallback is
// legitimate — it is a precision the document could offer and currently does not.
//
// Only on a receive that actually declares a correlationId: without one the receive is unfiltered and
// there is nothing to locate (that case has its own warning), and a send never matches anything.
func (v *Validator) validateCorrelationLocation(step *parser.Step) []ValidationError {
	if v.resolveStepCorrelationLocation == nil || strings.TrimSpace(step.CorrelationID) == "" {
		return nil
	}
	if action, known := v.stepAction(step); !known || action != "receive" {
		return nil
	}

	locations, source, resolved := v.resolveStepCorrelationLocation(step)
	if !resolved || len(locations) > 0 {
		return nil // unresolvable, or the document already says where to look
	}

	where := "the AsyncAPI source"
	if source != "" {
		where = fmt.Sprintf("source '%s'", source)
	}
	return []ValidationError{{
		Line:     step.LineNumber,
		Column:   0,
		Message:  fmt.Sprintf("Step '%s': no correlation id location is declared in %s, so the whole message is searched for the correlationId — a message that merely carries that value elsewhere can match, and the workflow would continue on the wrong one. Consider declaring 'correlationId.location' (e.g. '$message.header#/correlationId') on the channel's message in %s", step.StepID, where, where),
		Severity: "information",
	}}
}

// containsMediaType reports whether the document declares this wire format for the channel, comparing
// the way the runtime's registry keys on it so equivalent spellings never read as a disagreement.
func containsMediaType(declared []string, contentType string) bool {
	for _, d := range declared {
		if utils.SameMediaType(d, contentType) {
			return true
		}
	}
	return false
}

// validateDependsOn validates step-level dependsOn references (spec §5.8.3).
// Three forms are accepted:
//  1. Bare stepId — must exist in the same workflow
//  2. $workflows.<workflowId>.steps.<stepId> — both IDs must exist in this document
//  3. $sourceDescriptions.<name>.<workflowId>.steps.<stepId> — external; only format is checked
func (v *Validator) validateDependsOn(step *parser.Step, workflow *parser.Workflow, doc *parser.ArazzoDocument) []ValidationError {
	var errors []ValidationError
	for _, dep := range step.DependsOn {
		if dep == "" {
			continue
		}
		if dep == step.StepID {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': 'dependsOn' must not reference the step itself", step.StepID),
				Severity: "error",
			})
			continue
		}
		if strings.HasPrefix(dep, "$sourceDescriptions.") {
			// Form 3: $sourceDescriptions.<name>.<workflowId>.steps.<stepId>
			rest := strings.TrimPrefix(dep, "$sourceDescriptions.")
			stepsIdx := strings.Index(rest, ".steps.")
			if stepsIdx <= 0 {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': invalid dependsOn reference '%s' (external form must be '$sourceDescriptions.<name>.<workflowId>.steps.<stepId>')", step.StepID, dep),
					Severity: "error",
				})
				continue
			}
			prefixParts := strings.SplitN(rest[:stepsIdx], ".", 2)
			if len(prefixParts) < 2 || prefixParts[0] == "" || prefixParts[1] == "" {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': invalid dependsOn reference '%s' (external form must be '$sourceDescriptions.<name>.<workflowId>.steps.<stepId>')", step.StepID, dep),
					Severity: "error",
				})
			}
			refStepID := rest[stepsIdx+len(".steps."):]
			if refStepID == "" {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': invalid dependsOn reference '%s' (external form must be '$sourceDescriptions.<name>.<workflowId>.steps.<stepId>')", step.StepID, dep),
					Severity: "error",
				})
			}
			// External reference — cannot validate existence; format is valid
		} else if strings.HasPrefix(dep, "$workflows.") {
			// Form 2: $workflows.<workflowId>.steps.<stepId>
			rest := strings.TrimPrefix(dep, "$workflows.")
			stepsIdx := strings.Index(rest, ".steps.")
			if stepsIdx <= 0 {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': invalid dependsOn reference '%s' (cross-workflow form must be '$workflows.<workflowId>.steps.<stepId>')", step.StepID, dep),
					Severity: "error",
				})
				continue
			}
			refWfID := rest[:stepsIdx]
			refStepID := rest[stepsIdx+7:] // len(".steps.") == 7
			if refStepID == "" {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': invalid dependsOn reference '%s' (stepId part is empty)", step.StepID, dep),
					Severity: "error",
				})
				continue
			}
			refWf := v.parser.FindWorkflowByID(doc, refWfID)
			if refWf == nil {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': dependsOn references unknown workflow '%s'", step.StepID, refWfID),
					Severity: "error",
				})
			} else if v.parser.FindStepByID(refWf, refStepID) == nil {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': dependsOn references unknown step '%s' in workflow '%s'", step.StepID, refStepID, refWfID),
					Severity: "error",
				})
			}
		} else if strings.HasPrefix(dep, "$") {
			// Unrecognized expression form
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': invalid dependsOn reference '%s' (must be a bare stepId, '$workflows.<wfId>.steps.<stepId>', or '$sourceDescriptions.<name>.<wfId>.steps.<stepId>')", step.StepID, dep),
				Severity: "error",
			})
		} else {
			// Form 1: bare stepId in the same workflow
			found := false
			for _, s := range workflow.Steps {
				if s.StepID == dep {
					found = true
					break
				}
			}
			if !found {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': dependsOn references unknown step '%s' in the current workflow", step.StepID, dep),
					Severity: "error",
				})
			}
		}
	}
	return errors
}

// validateActionParameters validates Parameter Objects in SuccessAction and FailureAction.
// Per spec §5.8.7.1 (SuccessAction) and §5.8.8.1 (FailureAction):
//   - 'parameters' are ONLY meaningful when the action specifies a 'workflowId'
//   - the 'in' field MUST NOT be used (parameters map to workflow inputs, not HTTP operations)
//
// validateDependsOnCycles detects circular local dependsOn relationships within a single workflow
// (e.g. A dependsOn B, B dependsOn A), which have no resolvable execution order (spec §5.8.5.1).
// Only bare (local) stepId references participate; cross-workflow / cross-document forms are validated
// by validateDependsOn.
func (v *Validator) validateDependsOnCycles(workflow *parser.Workflow) []ValidationError {
	exists := make(map[string]bool)
	line := make(map[string]int)
	for _, step := range workflow.Steps {
		exists[step.StepID] = true
		line[step.StepID] = step.LineNumber
	}
	// edges: stepId -> the local stepIds it dependsOn
	edges := make(map[string][]string)
	for _, step := range workflow.Steps {
		for _, dep := range step.DependsOn {
			if dep == "" || strings.HasPrefix(dep, "$") {
				continue // cross-workflow/cross-document refs can't form a same-workflow cycle
			}
			if exists[dep] {
				edges[step.StepID] = append(edges[step.StepID], dep)
			}
		}
	}

	var errors []ValidationError
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	stateOf := make(map[string]int)
	reported := make(map[string]bool)

	var visit func(node string)
	visit = func(node string) {
		stateOf[node] = inStack
		for _, next := range edges[node] {
			switch stateOf[next] {
			case inStack: // back-edge → cycle
				key := node + "\x00" + next
				if !reported[key] {
					reported[key] = true
					errors = append(errors, ValidationError{
						Line:     line[node],
						Column:   0,
						Message:  fmt.Sprintf("Step '%s': circular dependsOn detected (part of a dependency cycle involving '%s')", node, next),
						Severity: "error",
					})
				}
			case unvisited:
				visit(next)
			}
		}
		stateOf[node] = done
	}

	for _, step := range workflow.Steps {
		if stateOf[step.StepID] == unvisited {
			visit(step.StepID)
		}
	}
	return errors
}

// validateWorkflowDependsOnCycles detects circular workflow-level dependsOn relationships across the
// whole document (e.g. workflow A dependsOn B, B dependsOn A), which have no resolvable execution
// order and would infinite-recurse at runtime (spec §5.8.4.1 — workflow dependsOn TRIGGERS the
// referenced workflows). This mirrors the runner's depStack cycle guard (runner.go executeDependencies).
// Only local workflowId references participate; cross-document ($sourceDescriptions.*) refs are
// resolved/executed elsewhere and cannot form an in-document cycle.
func (v *Validator) validateWorkflowDependsOnCycles(doc *parser.ArazzoDocument) []ValidationError {
	exists := make(map[string]bool)
	line := make(map[string]int)
	for _, wf := range doc.Workflows {
		exists[wf.WorkflowID] = true
		line[wf.WorkflowID] = wf.LineNumber
	}
	// edges: workflowId -> the local workflowIds it dependsOn
	edges := make(map[string][]string)
	for _, wf := range doc.Workflows {
		for _, dep := range wf.DependsOn {
			if dep == "" || strings.HasPrefix(dep, "$") {
				continue // cross-document refs can't form a same-document cycle
			}
			if exists[dep] {
				edges[wf.WorkflowID] = append(edges[wf.WorkflowID], dep)
			}
		}
	}

	var errors []ValidationError
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	stateOf := make(map[string]int)
	reported := make(map[string]bool)

	var visit func(node string)
	visit = func(node string) {
		stateOf[node] = inStack
		for _, next := range edges[node] {
			switch stateOf[next] {
			case inStack: // back-edge → cycle
				key := node + "\x00" + next
				if !reported[key] {
					reported[key] = true
					errors = append(errors, ValidationError{
						Line:     line[node],
						Column:   0,
						Message:  fmt.Sprintf("Workflow '%s': circular dependsOn detected (part of a dependency cycle involving '%s')", node, next),
						Severity: "error",
					})
				}
			case unvisited:
				visit(next)
			}
		}
		stateOf[node] = done
	}

	for _, wf := range doc.Workflows {
		if stateOf[wf.WorkflowID] == unvisited {
			visit(wf.WorkflowID)
		}
	}
	return errors
}

func (v *Validator) validateActionParameters(params []parser.Parameter, workflowID string, specRef string, actionRef string, stepID string, lineNumber int) []ValidationError {
	var errors []ValidationError
	if len(params) == 0 {
		return errors
	}
	if workflowID == "" {
		errors = append(errors, ValidationError{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': %s has 'parameters' but no 'workflowId' — parameters are only valid when the action references a workflow (spec %s)", stepID, actionRef, specRef),
			Severity: "error",
		})
		return errors
	}
	for i, param := range params {
		if param.In != "" {
			errors = append(errors, ValidationError{
				Line:     lineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': %s parameters[%d]: the 'in' field MUST NOT be used on action parameters (spec %s)", stepID, actionRef, i, specRef),
				Severity: "error",
			})
		}
	}
	return errors
}

// validParameterLocations are the allowed Parameter Object 'in' values (spec §5.8.6).
// 'querystring' was added in v1.1.0. Note: 'body' is NOT valid — use requestBody instead.
var validParameterLocations = map[string]bool{
	"path": true, "query": true, "querystring": true, "header": true, "cookie": true,
}

// allowedExprVersions lists the spec-permitted versions per Expression Type Object dialect (§5.8.12).
var allowedExprVersions = map[string]map[string]bool{
	"jsonpath":    {"rfc9535": true, "draft-goessner-dispatch-jsonpath-00": true},
	"xpath":       {"xpath-31": true, "xpath-30": true, "xpath-20": true, "xpath-10": true},
	"jsonpointer": {"rfc6901": true},
}

// validateExpressionType validates an Expression Type Object (spec §5.8.12) used as a criterion,
// selector, or replacement 'type'. A bare-string type (e.g. "jsonpath") needs no version check —
// defaults apply at runtime. When the type is an object, 'type' must be a known dialect and
// 'version' is REQUIRED and must be valid for that dialect.
func (v *Validator) validateExpressionType(typeField interface{}, contextLabel string, lineNumber int) []ValidationError {
	m, ok := typeField.(map[string]interface{})
	if !ok {
		return nil // bare string short form or absent — nothing to validate here
	}
	dialect, _ := m["type"].(string)
	versions, known := allowedExprVersions[dialect]
	if !known {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("%s: Expression Type Object has invalid 'type' '%s' (must be jsonpath, xpath, or jsonpointer)", contextLabel, dialect),
			Severity: "error",
		}}
	}
	version, _ := m["version"].(string)
	if version == "" {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("%s: Expression Type Object is missing required 'version' for type '%s'", contextLabel, dialect),
			Severity: "error",
		}}
	}
	if !versions[version] {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("%s: Expression Type Object has unsupported version '%s' for type '%s'", contextLabel, version, dialect),
			Severity: "error",
		}}
	}
	return nil
}

// validateParameterLocations checks that each parameter's 'in' value (when present) is a
// valid Arazzo parameter location. Reusable-object references (no inline 'in') are skipped.
func (v *Validator) validateParameterLocations(params []parser.Parameter, stepID string, lineNumber int) []ValidationError {
	var errors []ValidationError
	for _, param := range params {
		if param.In == "" {
			continue
		}
		if !validParameterLocations[param.In] {
			errors = append(errors, ValidationError{
				Line:     lineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': parameter '%s' has invalid 'in' value '%s' (must be 'path', 'query', 'querystring', 'header', or 'cookie')", stepID, param.Name, param.In),
				Severity: "error",
			})
		}
	}
	return errors
}

// validateActionType checks a Success/Failure Action 'type' against the allowed set.
func (v *Validator) validateActionType(actionType string, allowed []string, actionRef, stepID string, lineNumber int) []ValidationError {
	if actionType == "" {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': %s is missing required field 'type'", stepID, actionRef),
			Severity: "error",
		}}
	}
	for _, a := range allowed {
		if actionType == a {
			return nil
		}
	}
	return []ValidationError{{
		Line:     lineNumber,
		Column:   0,
		Message:  fmt.Sprintf("Step '%s': %s has invalid type '%s' (must be one of %s)", stepID, actionRef, actionType, strings.Join(allowed, ", ")),
		Severity: "error",
	}}
}

// validateActionTarget enforces that 'stepId' and 'workflowId' are mutually exclusive on an action.
func (v *Validator) validateActionTarget(stepIDTarget, workflowID, actionRef, stepID string, lineNumber int) []ValidationError {
	if stepIDTarget != "" && workflowID != "" {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': %s has both 'stepId' and 'workflowId' (they are mutually exclusive)", stepID, actionRef),
			Severity: "error",
		}}
	}
	return nil
}

// validateComponentReference checks that a Reusable Object 'reference' resolves to an existing
// component. References use the runtime expression form '$components.<section>.<key>'
// (spec §5.8.10 Reusable Object). When expectedSection is non-empty, the reference MUST point at
// that section (e.g. a parameter reference must use '$components.parameters.<key>'), so a
// miswired reference to a different section is rejected. Empty references are ignored (inline objects).
func (v *Validator) validateComponentReference(reference string, doc *parser.ArazzoDocument, expectedSection, stepID string, lineNumber int) []ValidationError {
	if reference == "" {
		return nil
	}
	if !strings.HasPrefix(reference, "$components.") {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': reusable-object reference '%s' must be a runtime expression of the form '$components.<section>.<key>'", stepID, reference),
			Severity: "warning",
		}}
	}
	rest := strings.TrimPrefix(reference, "$components.")
	dot := strings.Index(rest, ".")
	if dot <= 0 || dot == len(rest)-1 {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': malformed component reference '%s' (expected '$components.<section>.<key>')", stepID, reference),
			Severity: "error",
		}}
	}
	section, key := rest[:dot], rest[dot+1:]
	if expectedSection != "" && section != expectedSection {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': reference '%s' must point to '$components.%s.<key>' in this position", stepID, reference, expectedSection),
			Severity: "error",
		}}
	}
	if doc.Components == nil {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': reference '%s' but the document has no 'components' section", stepID, reference),
			Severity: "error",
		}}
	}
	exists := false
	switch section {
	case "inputs":
		_, exists = doc.Components.Inputs[key]
	case "parameters":
		_, exists = doc.Components.Parameters[key]
	case "successActions":
		_, exists = doc.Components.SuccessActions[key]
	case "failureActions":
		_, exists = doc.Components.FailureActions[key]
	default:
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': reference '%s' uses unknown components section '%s' (expected inputs, parameters, successActions, or failureActions)", stepID, reference, section),
			Severity: "error",
		}}
	}
	if !exists {
		return []ValidationError{{
			Line:     lineNumber,
			Column:   0,
			Message:  fmt.Sprintf("Step '%s': reference '%s' does not resolve — no '%s' named '%s' in components", stepID, reference, section, key),
			Severity: "error",
		}}
	}
	return nil
}
