package validator

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/arazzo/lsp/parser"
)

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
}

// NewValidator creates a new Validator
func NewValidator() *Validator {
	return &Validator{
		parser: parser.NewParser(),
	}
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

		// Validate action enum: if set, must be "send" or "receive" (spec §5.8.3)
		if step.Action != "" && step.Action != "send" && step.Action != "receive" {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': Invalid action '%s' (must be 'send' or 'receive')", step.StepID, step.Action),
				Severity: "error",
			})
		}

		// Validate channelPath format and source description type (spec §5.8.3)
		if step.ChannelPath != "" {
			parts := strings.SplitN(step.ChannelPath, "#", 2)
			if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
				errors = append(errors, ValidationError{
					Line:     step.LineNumber,
					Column:   0,
					Message:  fmt.Sprintf("Step '%s': 'channelPath' must be in the format '{sourceDescriptionName}#{channelPath}' (got '%s')", step.StepID, step.ChannelPath),
					Severity: "error",
				})
			} else {
				sdName := parts[0]
				sdFound := false
				for _, sd := range doc.SourceDescriptions {
					if sd.Name == sdName {
						sdFound = true
						if sd.Type != "asyncapi" {
							errors = append(errors, ValidationError{
								Line:     step.LineNumber,
								Column:   0,
								Message:  fmt.Sprintf("Step '%s': 'channelPath' references source '%s' which has type '%s', but must be 'asyncapi'", step.StepID, sdName, sd.Type),
								Severity: "error",
							})
						}
						break
					}
				}
				if !sdFound {
					errors = append(errors, ValidationError{
						Line:     step.LineNumber,
						Column:   0,
						Message:  fmt.Sprintf("Step '%s': 'channelPath' references unknown source description '%s'", step.StepID, sdName),
						Severity: "warning",
					})
				}
			}
		}

		// Validate timeout is non-negative (spec §5.8.3)
		if step.Timeout < 0 {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': 'timeout' must be a non-negative integer (milliseconds), got %d", step.StepID, step.Timeout),
				Severity: "error",
			})
		}

		// Validate dependsOn references (spec §5.8.3)
		errors = append(errors, v.validateDependsOn(&step, workflow, doc)...)

		// Warn if correlationId is set on a non-AsyncAPI step (no channelPath)
		if step.CorrelationID != "" && step.ChannelPath == "" {
			errors = append(errors, ValidationError{
				Line:     step.LineNumber,
				Column:   0,
				Message:  fmt.Sprintf("Step '%s': 'correlationId' is only meaningful on AsyncAPI steps (channelPath must also be set)", step.StepID),
				Severity: "warning",
			})
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

		// Validate onSuccess action parameters (spec §5.8.7.1):
		//   - parameters only valid when workflowId is set
		//   - 'in' MUST NOT be used
		for i, action := range step.OnSuccess {
			errors = append(errors, v.validateActionParameters(action.Parameters, action.WorkflowID, "Arazzo spec section 5.8.7.1", fmt.Sprintf("onSuccess[%d]", i), step.StepID, step.LineNumber)...)
		}

		// Validate onFailure action parameters (spec §5.8.8.1):
		//   - parameters only valid when workflowId is set
		//   - 'in' MUST NOT be used
		for i, action := range step.OnFailure {
			errors = append(errors, v.validateActionParameters(action.Parameters, action.WorkflowID, "Arazzo spec section 5.8.8.1", fmt.Sprintf("onFailure[%d]", i), step.StepID, step.LineNumber)...)
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
							// Check if referenced step exists and comes before this step
							if !v.stepExistsBeforeCurrent(workflow, refStepID, step.StepID) {
								errors = append(errors, ValidationError{
									Line:     step.LineNumber,
									Column:   0,
									Message:  fmt.Sprintf("Step '%s': Referenced step '%s' does not exist or comes after current step", step.StepID, refStepID),
									Severity: "error",
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
