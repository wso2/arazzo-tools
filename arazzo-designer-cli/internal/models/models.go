// Package models defines the core data structures for the Arazzo workflow runner.
// These mirror the Python arazzo-runner's models exactly.
package models

// StepStatus represents the status of a workflow step.
type StepStatus string

const (
	StepStatusPending StepStatus = "pending"
	StepStatusRunning StepStatus = "running"
	StepStatusSuccess StepStatus = "success"
	StepStatusFailure StepStatus = "failure"
	StepStatusSkipped StepStatus = "skipped"
)

// ActionType represents the type of action to take after a step.
type ActionType string

const (
	ActionTypeContinue ActionType = "continue"
	ActionTypeEnd      ActionType = "end"
	ActionTypeGoto     ActionType = "goto"
	ActionTypeRetry    ActionType = "retry"
)

// WorkflowExecutionStatus represents the status of a workflow execution.
type WorkflowExecutionStatus string

const (
	WorkflowStatusStepComplete     WorkflowExecutionStatus = "step_complete"
	WorkflowStatusStepError        WorkflowExecutionStatus = "step_error"
	WorkflowStatusWorkflowComplete WorkflowExecutionStatus = "workflow_complete"
	WorkflowStatusError            WorkflowExecutionStatus = "error"
	WorkflowStatusGotoStep         WorkflowExecutionStatus = "goto_step"
	WorkflowStatusGotoWorkflow     WorkflowExecutionStatus = "goto_workflow"
	WorkflowStatusRetry            WorkflowExecutionStatus = "retry"
)

// ExecutionState holds the state of a workflow execution.
// StepsData stores full step execution data (statusCode, response, outputs, errors).
// StepsStatus stores the status of each step.
// This mirrors the Python arazzo-runner's ExecutionState closely.
type ExecutionState struct {
	WorkflowID        string
	CurrentStepID     string
	Inputs            map[string]interface{}
	StepsData         map[string]interface{} // stepId -> {statusCode, response:{body,header,statusCode}, outputs:{...}, error:...}
	StepsStatus       map[string]StepStatus  // stepId -> status
	WorkflowOutputs   map[string]interface{}
	DependencyOutputs map[string]map[string]interface{} // workflowId -> outputs
	// DependencyStepStatus holds the per-step status of each workflow that ran as a dependency
	// (workflowId -> stepId -> status). It lets the step-level dependsOn gate verify that a SPECIFIC
	// step in another workflow completed successfully — e.g. "$workflows.<wf>.steps.<s>" — not merely
	// that the workflow ran (spec §5.8.5.1). Populated by the runner from each dependency's result.
	DependencyStepStatus map[string]map[string]StepStatus
	RuntimeParams        *RuntimeParams

	// v1.1.0 runtime-expression context (populated by the runner from the Arazzo document):
	//   Self                     -> resolves "$self" (the document's $self / canonical URI).
	//   Components               -> resolves "$components.<type>.<name>" (the Components Object).
	//   SourceDescriptionObjects -> name -> the Source Description Object ({name,url,type}); used for
	//                               "$sourceDescriptions.<name>.<field>" access (spec §5.9.2 fallback).
	//   WorkflowsByID            -> workflowId -> the workflow map; resolves "$workflows.<id>.<field>".
	Self                     string
	Components               map[string]interface{}
	SourceDescriptionObjects map[string]interface{}
	WorkflowsByID            map[string]interface{}

	// Trace context — populated by the runner so step/HTTP executors can
	// emit child spans under the workflow span.
	TraceID        string
	WorkflowSpanID string
}

// NewExecutionState creates a new ExecutionState with initialized maps.
func NewExecutionState(workflowID string, inputs map[string]interface{}, depOutputs map[string]map[string]interface{}, runtimeParams *RuntimeParams) *ExecutionState {
	if inputs == nil {
		inputs = make(map[string]interface{})
	}
	if depOutputs == nil {
		depOutputs = make(map[string]map[string]interface{})
	}
	return &ExecutionState{
		WorkflowID:           workflowID,
		Inputs:               inputs,
		StepsData:            make(map[string]interface{}),
		StepsStatus:          make(map[string]StepStatus),
		WorkflowOutputs:      make(map[string]interface{}),
		DependencyOutputs:    depOutputs,
		DependencyStepStatus: make(map[string]map[string]StepStatus),
		RuntimeParams:        runtimeParams,
	}
}

// WorkflowExecutionResult holds the result of a workflow execution.
type WorkflowExecutionResult struct {
	Status      WorkflowExecutionStatus           `json:"status"`
	WorkflowID  string                            `json:"workflow_id"`
	Outputs     map[string]interface{}            `json:"outputs"`
	StepOutputs map[string]map[string]interface{} `json:"step_outputs,omitempty"`
	// StepsStatus is the per-step terminal status (stepId -> status) of this run. It is surfaced so a
	// caller (notably executeDependencies) can tell whether a SPECIFIC step succeeded, which the
	// cross-workflow step-level dependsOn gate needs (spec §5.8.5.1).
	StepsStatus map[string]StepStatus  `json:"steps_status,omitempty"`
	Inputs      map[string]interface{} `json:"inputs,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

// RuntimeParams holds runtime parameters for workflow execution.
type RuntimeParams struct {
	ServerVariables        map[string]string    `json:"server_variables,omitempty"`
	BearerToken            string               `json:"bearer_token,omitempty"`
	APIKey                 string               `json:"api_key,omitempty"`
	APIKeyHeader           string               `json:"api_key_header,omitempty"`
	AuthHeaders            map[string]string    `json:"auth_headers,omitempty"`
	ServerConfig           *ServerConfiguration `json:"server_config,omitempty"`
	DisableTLSVerification bool                 `json:"disable_tls_verification,omitempty"`
}

// ServerVariable represents a server variable override with name and value.
type ServerVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ServerConfiguration represents a server configuration override.
type ServerConfiguration struct {
	URL         string           `json:"url,omitempty"`
	ServerIndex int              `json:"server_index,omitempty"`
	Variables   []ServerVariable `json:"variables,omitempty"`
}

// NextAction represents the next action to take after a step execution.
type NextAction struct {
	Type       ActionType
	StepID     string
	WorkflowID string
	RetryAfter float64
	RetryLimit int
	Inputs     map[string]interface{}
}

// StepResult holds the result of executing a single step.
type StepResult struct {
	StepID           string                 `json:"step_id"`
	Success          bool                   `json:"success"`
	StatusCode       int                    `json:"status_code,omitempty"`
	ResponseBody     interface{}            `json:"response_body,omitempty"`
	Headers          map[string]string      `json:"headers,omitempty"`
	Outputs          map[string]interface{} `json:"outputs,omitempty"`
	NextAction       *NextAction            `json:"next_action,omitempty"`
	Error            string                 `json:"error,omitempty"`
	IsNestedWorkflow bool                   `json:"is_nested_workflow,omitempty"`
}

// HTTPResponse represents an HTTP response from an API call.
type HTTPResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       interface{}       `json:"body"`
}

// OperationInfo contains details about a found OpenAPI operation.
type OperationInfo struct {
	Source      string                 `json:"source"`
	Path        string                 `json:"path"`
	Method      string                 `json:"method"`
	URL         string                 `json:"url"`
	Operation   map[string]interface{} `json:"operation"`
	OperationID string                 `json:"operationId,omitempty"`
}
