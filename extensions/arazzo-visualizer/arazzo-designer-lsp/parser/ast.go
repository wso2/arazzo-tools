package parser

// ArazzoDocument represents the root Arazzo specification document
type ArazzoDocument struct {
	Arazzo             string              `yaml:"arazzo" json:"arazzo"`
	Self               string              `yaml:"$self,omitempty" json:"$self,omitempty"` // v1.1.0: URL reference to the Arazzo description itself
	Info               Info                `yaml:"info" json:"info"`
	SourceDescriptions []SourceDescription `yaml:"sourceDescriptions" json:"sourceDescriptions"`
	Workflows          []Workflow          `yaml:"workflows" json:"workflows"`
	Components         *Components         `yaml:"components,omitempty" json:"components,omitempty"`
	LineMap            map[string]int      `yaml:"-" json:"-"` // Maps element IDs to line numbers
}

// Info provides metadata about the Arazzo document
type Info struct {
	Title       string `yaml:"title" json:"title"`
	Summary     string `yaml:"summary,omitempty" json:"summary,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Version     string `yaml:"version" json:"version"`
}

// SourceDescription references an API description (OpenAPI, AsyncAPI, or Arazzo)
type SourceDescription struct {
	Name string                 `yaml:"name" json:"name"`
	URL  string                 `yaml:"url" json:"url"`
	Type string                 `yaml:"type,omitempty" json:"type,omitempty"` // "openapi", "asyncapi", or "arazzo"
	Ext  map[string]interface{} `yaml:",inline" json:"-"`
}

// Workflow represents a sequence of steps
type Workflow struct {
	WorkflowID     string                 `yaml:"workflowId" json:"workflowId"`
	Summary        string                 `yaml:"summary,omitempty" json:"summary,omitempty"`
	Description    string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Inputs         interface{}            `yaml:"inputs,omitempty" json:"inputs,omitempty"` // JSON Schema
	DependsOn      []string               `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
	Steps          []Step                 `yaml:"steps" json:"steps"`
	Parameters     []Parameter            `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	SuccessActions []SuccessAction        `yaml:"successActions,omitempty" json:"successActions,omitempty"`
	FailureActions []FailureAction        `yaml:"failureActions,omitempty" json:"failureActions,omitempty"`
	Outputs        map[string]interface{} `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	LineNumber     int                    `yaml:"-" json:"-"` // Line number where workflow starts
}

// Step represents a single action in a workflow
type Step struct {
	StepID          string                 `yaml:"stepId" json:"stepId"`
	Description     string                 `yaml:"description,omitempty" json:"description,omitempty"`
	OperationID     string                 `yaml:"operationId,omitempty" json:"operationId,omitempty"`
	OperationPath   string                 `yaml:"operationPath,omitempty" json:"operationPath,omitempty"`
	WorkflowID      string                 `yaml:"workflowId,omitempty" json:"workflowId,omitempty"`
	ChannelPath     string                 `yaml:"channelPath,omitempty" json:"channelPath,omitempty"`     // v1.1.0: AsyncAPI channel reference
	Timeout         int                    `yaml:"timeout,omitempty" json:"timeout,omitempty"`             // v1.1.0: max execution time in ms
	CorrelationID   string                 `yaml:"correlationId,omitempty" json:"correlationId,omitempty"` // v1.1.0: AsyncAPI correlation ID
	Action          string                 `yaml:"action,omitempty" json:"action,omitempty"`               // v1.1.0: "send" or "receive"
	DependsOn       []string               `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`         // v1.1.0: step-level prerequisites
	Parameters      []Parameter            `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	RequestBody     *RequestBody           `yaml:"requestBody,omitempty" json:"requestBody,omitempty"`
	SuccessCriteria []Criterion            `yaml:"successCriteria,omitempty" json:"successCriteria,omitempty"`
	OnSuccess       []SuccessAction        `yaml:"onSuccess,omitempty" json:"onSuccess,omitempty"`
	OnFailure       []FailureAction        `yaml:"onFailure,omitempty" json:"onFailure,omitempty"`
	Outputs         map[string]interface{} `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	LineNumber      int                    `yaml:"-" json:"-"` // Line number where step starts
}

// Parameter represents a parameter for a step (or a Reusable Object referencing one)
type Parameter struct {
	Reference string      `yaml:"reference,omitempty" json:"reference,omitempty"` // Arazzo reusable-object reference, e.g. $components.parameters.RequestId
	Name      string      `yaml:"name,omitempty" json:"name,omitempty"`
	In        string      `yaml:"in,omitempty" json:"in,omitempty"`       // path, query, querystring, header, cookie (spec §5.8.6; 'querystring' added v1.1.0)
	Value     interface{} `yaml:"value,omitempty" json:"value,omitempty"` // Can be a literal value or runtime expression
}

// RequestBody defines the request body for a step
type RequestBody struct {
	ContentType  string        `yaml:"contentType,omitempty" json:"contentType,omitempty"`
	Payload      interface{}   `yaml:"payload,omitempty" json:"payload,omitempty"`
	Replacements []Replacement `yaml:"replacements,omitempty" json:"replacements,omitempty"` // v1.1.0: payload replacements
}

// Replacement defines a location within a payload and a value to set (v1.1.0: Payload Replacement Object)
type Replacement struct {
	Target             string      `yaml:"target" json:"target"`                                             // REQUIRED. JSON Pointer, XPath, or JSONPath to the location to replace
	TargetSelectorType interface{} `yaml:"targetSelectorType,omitempty" json:"targetSelectorType,omitempty"` // optional: string or ExpressionTypeObject
	Value              interface{} `yaml:"value" json:"value"`                                               // REQUIRED. constant, expression, or Selector Object
}

// Criterion defines success/failure conditions (spec §5.8.11)
type Criterion struct {
	Context   string      `yaml:"context,omitempty" json:"context,omitempty"` // Runtime expression setting the evaluation context
	Condition string      `yaml:"condition" json:"condition"`                 // REQUIRED. Condition expression to evaluate
	Type      interface{} `yaml:"type,omitempty" json:"type,omitempty"`       // optional: "simple"|"regex"|"jsonpath"|"xpath" or ExpressionTypeObject
}

// SuccessAction defines actions to take on step success (or a Reusable Object referencing one)
type SuccessAction struct {
	Reference  string                 `yaml:"reference,omitempty" json:"reference,omitempty"` // Arazzo reusable-object reference, e.g. $components.successActions.AuditSuccess
	Name       string                 `yaml:"name,omitempty" json:"name,omitempty"`
	Type       string                 `yaml:"type,omitempty" json:"type,omitempty"` // goto, end
	StepID     string                 `yaml:"stepId,omitempty" json:"stepId,omitempty"`
	WorkflowID string                 `yaml:"workflowId,omitempty" json:"workflowId,omitempty"`
	Parameters []Parameter            `yaml:"parameters,omitempty" json:"parameters,omitempty"` // v1.1.0: parameters passed to referenced workflow
	Criteria   []Criterion            `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	Ext        map[string]interface{} `yaml:",inline" json:"-"`
}

// FailureAction defines actions to take on step failure (or a Reusable Object referencing one)
type FailureAction struct {
	Reference  string                 `yaml:"reference,omitempty" json:"reference,omitempty"` // Arazzo reusable-object reference, e.g. $components.failureActions.RetryOn503
	Name       string                 `yaml:"name,omitempty" json:"name,omitempty"`
	Type       string                 `yaml:"type,omitempty" json:"type,omitempty"` // retry, goto, end
	StepID     string                 `yaml:"stepId,omitempty" json:"stepId,omitempty"`
	WorkflowID string                 `yaml:"workflowId,omitempty" json:"workflowId,omitempty"`
	RetryAfter float64                `yaml:"retryAfter,omitempty" json:"retryAfter,omitempty"` // seconds
	RetryLimit int                    `yaml:"retryLimit,omitempty" json:"retryLimit,omitempty"`
	Parameters []Parameter            `yaml:"parameters,omitempty" json:"parameters,omitempty"` // v1.1.0: parameters passed to referenced workflow
	Criteria   []Criterion            `yaml:"criteria,omitempty" json:"criteria,omitempty"`
	Ext        map[string]interface{} `yaml:",inline" json:"-"`
}

// SelectorObject enables fine-grained traversal of structured data (spec §5.8.13)
type SelectorObject struct {
	Context  string      `yaml:"context" json:"context"`   // REQUIRED. Runtime expression evaluating to structured data
	Selector string      `yaml:"selector" json:"selector"` // REQUIRED. JSONPath, XPath, or JSON Pointer expression
	Type     interface{} `yaml:"type" json:"type"`         // REQUIRED. "jsonpath"|"xpath"|"jsonpointer" or ExpressionTypeObject
}

// Components holds reusable objects (spec §5.8.9)
type Components struct {
	Inputs         map[string]interface{}   `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Parameters     map[string]Parameter     `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	SuccessActions map[string]SuccessAction `yaml:"successActions,omitempty" json:"successActions,omitempty"`
	FailureActions map[string]FailureAction `yaml:"failureActions,omitempty" json:"failureActions,omitempty"`
}
