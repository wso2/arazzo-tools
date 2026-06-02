package completion

import (
	"strings"

	"github.com/arazzo/lsp/parser"
	"go.lsp.dev/protocol"
)

// CompletionProvider provides code completion for Arazzo documents
type CompletionProvider struct {
	parser *parser.Parser
}

// NewCompletionProvider creates a new CompletionProvider
func NewCompletionProvider() *CompletionProvider {
	return &CompletionProvider{
		parser: parser.NewParser(),
	}
}

// ProvideCompletion generates completion items based on the current position
func (c *CompletionProvider) ProvideCompletion(content string, line, character int) []protocol.CompletionItem {
	var items []protocol.CompletionItem

	// Get the current line
	lines := strings.Split(content, "\n")
	if line < 0 || line >= len(lines) {
		return items
	}

	currentLine := lines[line]
	beforeCursor := currentLine[:min(character, len(currentLine))]

	// Detect YAML context (what object we're inside)
	context := c.detectContext(lines, line)

	// Determine context and provide appropriate completions
	switch {
	case strings.HasSuffix(beforeCursor, "$"):
		// Runtime expression completions
		items = append(items, c.getRuntimeExpressionCompletions()...)

	case strings.Contains(beforeCursor, "$steps."):
		// Step reference completions
		doc, err := c.parser.Parse(content)
		if err == nil {
			items = append(items, c.getStepReferenceCompletions(doc, beforeCursor)...)
		}

	case strings.Contains(beforeCursor, "$workflows."):
		// Workflow reference completions
		doc, err := c.parser.Parse(content)
		if err == nil {
			items = append(items, c.getWorkflowReferenceCompletions(doc)...)
		}

	case isAfterColon(beforeCursor):
		// Field value completions
		items = append(items, c.getFieldValueCompletions(beforeCursor)...)

	default:
		// Field name completions based on context
		items = append(items, c.getContextualCompletions(context, beforeCursor)...)
	}

	return items
}

// detectContext determines what YAML object/section we're currently in
func (c *CompletionProvider) detectContext(lines []string, currentLine int) string {
	// Walk backwards from current line to find the parent context
	currentIndent := getIndentation(lines[currentLine])

	for i := currentLine - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		lineIndent := getIndentation(line)

		// If we find a line with less indentation, it's our parent context
		if lineIndent < currentIndent {
			// Check what this parent is
			if strings.HasPrefix(trimmed, "info:") {
				return "info"
			} else if strings.HasPrefix(trimmed, "sourceDescriptions:") {
				return "sourceDescriptions"
			} else if strings.HasPrefix(trimmed, "workflows:") || strings.Contains(trimmed, "workflowId:") {
				return "workflow"
			} else if strings.HasPrefix(trimmed, "steps:") || strings.Contains(trimmed, "stepId:") {
				return "step"
			} else if strings.HasPrefix(trimmed, "parameters:") {
				return "parameters"
			} else if strings.HasPrefix(trimmed, "components:") {
				return "components"
			} else if strings.HasPrefix(trimmed, "requestBody:") {
				return "requestBody"
			} else if strings.HasPrefix(trimmed, "replacements:") {
				return "replacements"
			} else if strings.HasPrefix(trimmed, "onSuccess:") {
				return "onSuccess"
			} else if strings.HasPrefix(trimmed, "onFailure:") {
				return "onFailure"
			} else if strings.HasPrefix(trimmed, "successCriteria:") {
				return "successCriteria"
			} else if strings.Contains(trimmed, "- ") {
				// We're in an array, continue searching for the parent
				currentIndent = lineIndent
				continue
			}
		}
	}

	return "root"
}

// getIndentation returns the number of leading spaces in a line
func getIndentation(line string) int {
	count := 0
	for _, char := range line {
		if char == ' ' {
			count++
		} else if char == '\t' {
			count += 2 // Treat tab as 2 spaces
		} else {
			break
		}
	}
	return count
}

// getRuntimeExpressionCompletions returns runtime expression options
func (c *CompletionProvider) getRuntimeExpressionCompletions() []protocol.CompletionItem {
	return []protocol.CompletionItem{
		{
			Label:         "inputs",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to workflow inputs",
			Documentation: "Access input parameters defined for the workflow",
			InsertText:    "inputs.",
		},
		{
			Label:         "steps",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to previous steps",
			Documentation: "Access outputs from previous steps in the workflow",
			InsertText:    "steps.",
		},
		{
			Label:         "workflows",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to other workflows",
			Documentation: "Access outputs from other workflows",
			InsertText:    "workflows.",
		},
		{
			Label:         "statusCode",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "HTTP status code",
			Documentation: "The HTTP status code of the response",
			InsertText:    "statusCode",
		},
		{
			Label:         "response.body",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Response body",
			Documentation: "The response body from the API call",
			InsertText:    "response.body",
		},
		{
			Label:         "response.header",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Response headers",
			Documentation: "The response headers from the API call",
			InsertText:    "response.header",
		},
		{
			Label:         "request.body",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Request body",
			Documentation: "The request body sent to the API",
			InsertText:    "request.body",
		},
		{
			Label:         "self",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to this Arazzo document (v1.1.0)",
			Documentation: "A URI-reference identifying this Arazzo document",
			InsertText:    "self",
		},
		{
			Label:         "message.payload",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "AsyncAPI message payload (v1.1.0)",
			Documentation: "The payload of the received AsyncAPI message",
			InsertText:    "message.payload",
		},
		{
			Label:         "message.header.",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "AsyncAPI message header (v1.1.0)",
			Documentation: "A header from the AsyncAPI message (append header name)",
			InsertText:    "message.header.",
		},
		{
			Label:         "sourceDescriptions.",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to a source description attribute",
			Documentation: "Access attributes of a named source description (e.g. $sourceDescriptions.petstore.url)",
			InsertText:    "sourceDescriptions.",
		},
		{
			Label:         "components.successActions.",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to a reusable success action (v1.1.0)",
			Documentation: "Access a reusable success action from components",
			InsertText:    "components.successActions.",
		},
		{
			Label:         "components.failureActions.",
			Kind:          protocol.CompletionItemKindVariable,
			Detail:        "Reference to a reusable failure action (v1.1.0)",
			Documentation: "Access a reusable failure action from components",
			InsertText:    "components.failureActions.",
		},
	}
}

// getStepReferenceCompletions returns step IDs for completion
func (c *CompletionProvider) getStepReferenceCompletions(doc *parser.ArazzoDocument, beforeCursor string) []protocol.CompletionItem {
	var items []protocol.CompletionItem

	// Extract which workflow context we're in
	for _, workflow := range doc.Workflows {
		for _, step := range workflow.Steps {
			items = append(items, protocol.CompletionItem{
				Label:         step.StepID,
				Kind:          protocol.CompletionItemKindReference,
				Detail:        "Step ID",
				Documentation: step.Description,
				InsertText:    step.StepID + ".outputs.",
			})
		}
	}

	return items
}

// getWorkflowReferenceCompletions returns workflow IDs for completion
func (c *CompletionProvider) getWorkflowReferenceCompletions(doc *parser.ArazzoDocument) []protocol.CompletionItem {
	var items []protocol.CompletionItem

	for _, workflow := range doc.Workflows {
		items = append(items, protocol.CompletionItem{
			Label:         workflow.WorkflowID,
			Kind:          protocol.CompletionItemKindReference,
			Detail:        "Workflow ID",
			Documentation: workflow.Description,
			InsertText:    workflow.WorkflowID + ".outputs.",
		})
	}

	return items
}

// getFieldValueCompletions returns completions for field values
func (c *CompletionProvider) getFieldValueCompletions(beforeCursor string) []protocol.CompletionItem {
	var items []protocol.CompletionItem

	if strings.Contains(beforeCursor, "arazzo:") {
		items = append(items,
			protocol.CompletionItem{Label: "\"1.1.0\"", Kind: protocol.CompletionItemKindValue, Detail: "Latest version (recommended)", InsertText: "\"1.1.0\""},
			protocol.CompletionItem{Label: "\"1.0.1\"", Kind: protocol.CompletionItemKindValue, InsertText: "\"1.0.1\""},
			protocol.CompletionItem{Label: "\"1.0.0\"", Kind: protocol.CompletionItemKindValue, InsertText: "\"1.0.0\""},
		)
	}

	if strings.Contains(beforeCursor, "type:") {
		items = append(items,
			protocol.CompletionItem{Label: "openapi", Kind: protocol.CompletionItemKindValue, InsertText: "openapi"},
			protocol.CompletionItem{Label: "asyncapi", Kind: protocol.CompletionItemKindValue, InsertText: "asyncapi"},
			protocol.CompletionItem{Label: "arazzo", Kind: protocol.CompletionItemKindValue, InsertText: "arazzo"},
		)
	}

	if strings.Contains(beforeCursor, "action:") {
		items = append(items,
			protocol.CompletionItem{Label: "send", Kind: protocol.CompletionItemKindValue, InsertText: "send"},
			protocol.CompletionItem{Label: "receive", Kind: protocol.CompletionItemKindValue, InsertText: "receive"},
		)
	}

	if strings.Contains(beforeCursor, "in:") {
		items = append(items,
			protocol.CompletionItem{Label: "query", Kind: protocol.CompletionItemKindValue, InsertText: "query"},
			protocol.CompletionItem{Label: "querystring", Kind: protocol.CompletionItemKindValue, InsertText: "querystring"},
			protocol.CompletionItem{Label: "header", Kind: protocol.CompletionItemKindValue, InsertText: "header"},
			protocol.CompletionItem{Label: "path", Kind: protocol.CompletionItemKindValue, InsertText: "path"},
			protocol.CompletionItem{Label: "cookie", Kind: protocol.CompletionItemKindValue, InsertText: "cookie"},
			protocol.CompletionItem{Label: "body", Kind: protocol.CompletionItemKindValue, InsertText: "body"},
		)
	}

	return items
}

// getTopLevelCompletions returns top-level field completions
func (c *CompletionProvider) getTopLevelCompletions() []protocol.CompletionItem {
	return []protocol.CompletionItem{
		{Label: "arazzo", Kind: protocol.CompletionItemKindField, Detail: "Arazzo version", InsertText: "arazzo: \"1.1.0\""},
		{Label: "info", Kind: protocol.CompletionItemKindField, Detail: "Metadata about the document", InsertText: "info:\n  title: \n  version: "},
		{Label: "sourceDescriptions", Kind: protocol.CompletionItemKindField, Detail: "API descriptions", InsertText: "sourceDescriptions:\n  - name: \n    url: \n    type: openapi"},
		{Label: "workflows", Kind: protocol.CompletionItemKindField, Detail: "Workflow definitions", InsertText: "workflows:\n  - workflowId: \n    steps:\n      - stepId: "},
		{Label: "components", Kind: protocol.CompletionItemKindField, Detail: "Reusable components", InsertText: "components:\n  inputs:\n  parameters:"},
	}
}

// getFieldNameCompletions returns field name completions based on context
func (c *CompletionProvider) getFieldNameCompletions(beforeCursor string) []protocol.CompletionItem {
	var items []protocol.CompletionItem

	// Workflow-level fields
	items = append(items,
		protocol.CompletionItem{Label: "workflowId", Kind: protocol.CompletionItemKindField, Detail: "Unique workflow identifier", InsertText: "workflowId: "},
		protocol.CompletionItem{Label: "summary", Kind: protocol.CompletionItemKindField, Detail: "Short summary", InsertText: "summary: "},
		protocol.CompletionItem{Label: "description", Kind: protocol.CompletionItemKindField, Detail: "Detailed description", InsertText: "description: "},
		protocol.CompletionItem{Label: "inputs", Kind: protocol.CompletionItemKindField, Detail: "Input parameters", InsertText: "inputs:\n  type: object\n  properties:"},
		protocol.CompletionItem{Label: "steps", Kind: protocol.CompletionItemKindField, Detail: "Workflow steps", InsertText: "steps:\n  - stepId: "},
		protocol.CompletionItem{Label: "outputs", Kind: protocol.CompletionItemKindField, Detail: "Output values", InsertText: "outputs:\n  "},
	)

	// Step-level fields
	items = append(items,
		protocol.CompletionItem{Label: "stepId", Kind: protocol.CompletionItemKindField, Detail: "Unique step identifier", InsertText: "stepId: "},
		protocol.CompletionItem{Label: "operationId", Kind: protocol.CompletionItemKindField, Detail: "OpenAPI operation ID", InsertText: "operationId: "},
		protocol.CompletionItem{Label: "operationPath", Kind: protocol.CompletionItemKindField, Detail: "Operation path reference", InsertText: "operationPath: "},
		protocol.CompletionItem{Label: "parameters", Kind: protocol.CompletionItemKindField, Detail: "Step parameters", InsertText: "parameters:\n  - name: \n    in: query\n    value: "},
		protocol.CompletionItem{Label: "requestBody", Kind: protocol.CompletionItemKindField, Detail: "Request body", InsertText: "requestBody:\n  contentType: application/json\n  payload:\n    "},
		protocol.CompletionItem{Label: "successCriteria", Kind: protocol.CompletionItemKindField, Detail: "Success conditions", InsertText: "successCriteria:\n  - condition: $statusCode == 200"},
		protocol.CompletionItem{Label: "onSuccess", Kind: protocol.CompletionItemKindField, Detail: "Success actions", InsertText: "onSuccess:\n  - name: \n    type: "},
		protocol.CompletionItem{Label: "onFailure", Kind: protocol.CompletionItemKindField, Detail: "Failure actions", InsertText: "onFailure:\n  - name: \n    type: "},
	)

	return items
}

// isAfterColon checks if the cursor is after a colon (in a value position)
func isAfterColon(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.Contains(trimmed, ":") && strings.HasSuffix(trimmed, ":")
}

// getContextualCompletions returns context-specific field completions
func (c *CompletionProvider) getContextualCompletions(context string, beforeCursor string) []protocol.CompletionItem {
	var items []protocol.CompletionItem

	switch context {
	case "info":
		// Info object fields
		items = append(items,
			protocol.CompletionItem{Label: "title", Kind: protocol.CompletionItemKindField, Detail: "Title of the document", InsertText: "title: "},
			protocol.CompletionItem{Label: "version", Kind: protocol.CompletionItemKindField, Detail: "Version of the document", InsertText: "version: "},
			protocol.CompletionItem{Label: "summary", Kind: protocol.CompletionItemKindField, Detail: "Short summary", InsertText: "summary: "},
			protocol.CompletionItem{Label: "description", Kind: protocol.CompletionItemKindField, Detail: "Detailed description", InsertText: "description: "},
		)

	case "sourceDescriptions":
		// Source description fields
		items = append(items,
			protocol.CompletionItem{Label: "name", Kind: protocol.CompletionItemKindField, Detail: "Name of the source", InsertText: "name: "},
			protocol.CompletionItem{Label: "url", Kind: protocol.CompletionItemKindField, Detail: "URL to the source document", InsertText: "url: "},
			protocol.CompletionItem{Label: "type", Kind: protocol.CompletionItemKindField, Detail: "Type of source (openapi or arazzo)", InsertText: "type: "},
			protocol.CompletionItem{Label: "x-", Kind: protocol.CompletionItemKindField, Detail: "Extension field", InsertText: "x-"},
		)

	case "workflow":
		// Workflow-level fields
		items = append(items,
			protocol.CompletionItem{Label: "workflowId", Kind: protocol.CompletionItemKindField, Detail: "Unique workflow identifier", InsertText: "workflowId: "},
			protocol.CompletionItem{Label: "summary", Kind: protocol.CompletionItemKindField, Detail: "Short summary", InsertText: "summary: "},
			protocol.CompletionItem{Label: "description", Kind: protocol.CompletionItemKindField, Detail: "Detailed description", InsertText: "description: "},
			protocol.CompletionItem{Label: "inputs", Kind: protocol.CompletionItemKindField, Detail: "Input parameters", InsertText: "inputs:\n  type: object\n  properties:\n    "},
			protocol.CompletionItem{Label: "steps", Kind: protocol.CompletionItemKindField, Detail: "Workflow steps", InsertText: "steps:\n  - stepId: "},
			protocol.CompletionItem{Label: "outputs", Kind: protocol.CompletionItemKindField, Detail: "Output values", InsertText: "outputs:\n  "},
			protocol.CompletionItem{Label: "parameters", Kind: protocol.CompletionItemKindField, Detail: "Reusable parameters", InsertText: "parameters:\n  - name: \n    in: query\n    value: "},
			protocol.CompletionItem{Label: "dependsOn", Kind: protocol.CompletionItemKindField, Detail: "Workflow dependencies", InsertText: "dependsOn:\n  - "},
			protocol.CompletionItem{Label: "successActions", Kind: protocol.CompletionItemKindField, Detail: "Reusable success actions for the workflow", InsertText: "successActions:\n  - name: \n    type: "},
			protocol.CompletionItem{Label: "failureActions", Kind: protocol.CompletionItemKindField, Detail: "Reusable failure actions for the workflow", InsertText: "failureActions:\n  - name: \n    type: "},
		)

	case "step":
		// Step-level fields
		items = append(items,
			protocol.CompletionItem{Label: "stepId", Kind: protocol.CompletionItemKindField, Detail: "Unique step identifier", InsertText: "stepId: "},
			protocol.CompletionItem{Label: "description", Kind: protocol.CompletionItemKindField, Detail: "Step description", InsertText: "description: "},
			protocol.CompletionItem{Label: "operationId", Kind: protocol.CompletionItemKindField, Detail: "OpenAPI operation ID", InsertText: "operationId: "},
			protocol.CompletionItem{Label: "operationPath", Kind: protocol.CompletionItemKindField, Detail: "Operation path reference", InsertText: "operationPath: "},
			protocol.CompletionItem{Label: "channelPath", Kind: protocol.CompletionItemKindField, Detail: "AsyncAPI channel reference (v1.1.0)", InsertText: "channelPath: "},
			protocol.CompletionItem{Label: "workflowId", Kind: protocol.CompletionItemKindField, Detail: "Reference to another workflow", InsertText: "workflowId: "},
			protocol.CompletionItem{Label: "timeout", Kind: protocol.CompletionItemKindField, Detail: "Max execution time in milliseconds (v1.1.0)", InsertText: "timeout: "},
			protocol.CompletionItem{Label: "correlationId", Kind: protocol.CompletionItemKindField, Detail: "AsyncAPI correlation ID expression (v1.1.0)", InsertText: "correlationId: "},
			protocol.CompletionItem{Label: "action", Kind: protocol.CompletionItemKindField, Detail: "AsyncAPI message direction: send or receive (v1.1.0)", InsertText: "action: send"},
			protocol.CompletionItem{Label: "dependsOn", Kind: protocol.CompletionItemKindField, Detail: "Step prerequisites (v1.1.0)", InsertText: "dependsOn:\n  - "},
			protocol.CompletionItem{Label: "parameters", Kind: protocol.CompletionItemKindField, Detail: "Step parameters", InsertText: "parameters:\n  - name: \n    in: query\n    value: "},
			protocol.CompletionItem{Label: "requestBody", Kind: protocol.CompletionItemKindField, Detail: "Request body", InsertText: "requestBody:\n  contentType: application/json\n  payload:\n    "},
			protocol.CompletionItem{Label: "successCriteria", Kind: protocol.CompletionItemKindField, Detail: "Success conditions", InsertText: "successCriteria:\n  - condition: $statusCode == 200"},
			protocol.CompletionItem{Label: "onSuccess", Kind: protocol.CompletionItemKindField, Detail: "Success actions", InsertText: "onSuccess:\n  - name: \n    type: "},
			protocol.CompletionItem{Label: "onFailure", Kind: protocol.CompletionItemKindField, Detail: "Failure actions", InsertText: "onFailure:\n  - name: \n    type: "},
			protocol.CompletionItem{Label: "outputs", Kind: protocol.CompletionItemKindField, Detail: "Step outputs", InsertText: "outputs:\n  "},
		)

	case "parameters":
		// Parameter fields
		items = append(items,
			protocol.CompletionItem{Label: "name", Kind: protocol.CompletionItemKindField, Detail: "Parameter name", InsertText: "name: "},
			protocol.CompletionItem{Label: "in", Kind: protocol.CompletionItemKindField, Detail: "Parameter location", InsertText: "in: "},
			protocol.CompletionItem{Label: "value", Kind: protocol.CompletionItemKindField, Detail: "Parameter value", InsertText: "value: "},
			protocol.CompletionItem{Label: "target", Kind: protocol.CompletionItemKindField, Detail: "Target parameter name", InsertText: "target: "},
		)

	case "components":
		// Components section fields
		items = append(items,
			protocol.CompletionItem{Label: "inputs", Kind: protocol.CompletionItemKindField, Detail: "Reusable inputs", InsertText: "inputs:\n  "},
			protocol.CompletionItem{Label: "parameters", Kind: protocol.CompletionItemKindField, Detail: "Reusable parameters", InsertText: "parameters:\n  "},
			protocol.CompletionItem{Label: "successActions", Kind: protocol.CompletionItemKindField, Detail: "Reusable success actions", InsertText: "successActions:\n  "},
			protocol.CompletionItem{Label: "failureActions", Kind: protocol.CompletionItemKindField, Detail: "Reusable failure actions", InsertText: "failureActions:\n  "},
		)

	case "requestBody":
		// Request body fields
		items = append(items,
			protocol.CompletionItem{Label: "contentType", Kind: protocol.CompletionItemKindField, Detail: "Media type of the request body", InsertText: "contentType: application/json"},
			protocol.CompletionItem{Label: "payload", Kind: protocol.CompletionItemKindField, Detail: "Request body payload", InsertText: "payload:\n  "},
			protocol.CompletionItem{Label: "replacements", Kind: protocol.CompletionItemKindField, Detail: "Payload replacements (v1.1.0)", InsertText: "replacements:\n  - target: \n    value: "},
		)

	case "replacements":
		// Payload Replacement Object fields (v1.1.0 spec §5.8.12)
		items = append(items,
			protocol.CompletionItem{Label: "target", Kind: protocol.CompletionItemKindField, Detail: "JSON Pointer, XPath, or JSONPath to the location to replace (REQUIRED)", InsertText: "target: "},
			protocol.CompletionItem{Label: "value", Kind: protocol.CompletionItemKindField, Detail: "Replacement value: constant, expression, or Selector Object (REQUIRED)", InsertText: "value: "},
			protocol.CompletionItem{Label: "targetSelectorType", Kind: protocol.CompletionItemKindField, Detail: "Selector type for the target pointer (v1.1.0)", InsertText: "targetSelectorType: "},
		)

	case "onSuccess":
		// Success Action Object fields (spec §5.8.7)
		items = append(items,
			protocol.CompletionItem{Label: "name", Kind: protocol.CompletionItemKindField, Detail: "Action name (REQUIRED)", InsertText: "name: "},
			protocol.CompletionItem{Label: "type", Kind: protocol.CompletionItemKindField, Detail: "Action type: goto or end (REQUIRED)", InsertText: "type: "},
			protocol.CompletionItem{Label: "stepId", Kind: protocol.CompletionItemKindField, Detail: "Target step ID (for goto — mutually exclusive with workflowId)", InsertText: "stepId: "},
			protocol.CompletionItem{Label: "workflowId", Kind: protocol.CompletionItemKindField, Detail: "Target workflow ID (for goto — mutually exclusive with stepId)", InsertText: "workflowId: "},
			protocol.CompletionItem{Label: "criteria", Kind: protocol.CompletionItemKindField, Detail: "Conditions that must be met for this action to apply", InsertText: "criteria:\n  - condition: "},
			protocol.CompletionItem{Label: "parameters", Kind: protocol.CompletionItemKindField, Detail: "Parameters for referenced workflow (workflowId only) — 'in' MUST NOT be used (spec §5.8.7.1)", InsertText: "parameters:\n  - name: \n    value: "},
		)

	case "onFailure":
		// Failure Action Object fields (spec §5.8.8)
		items = append(items,
			protocol.CompletionItem{Label: "name", Kind: protocol.CompletionItemKindField, Detail: "Action name (REQUIRED)", InsertText: "name: "},
			protocol.CompletionItem{Label: "type", Kind: protocol.CompletionItemKindField, Detail: "Action type: retry, goto, or end (REQUIRED)", InsertText: "type: "},
			protocol.CompletionItem{Label: "stepId", Kind: protocol.CompletionItemKindField, Detail: "Target step ID (for goto — mutually exclusive with workflowId)", InsertText: "stepId: "},
			protocol.CompletionItem{Label: "workflowId", Kind: protocol.CompletionItemKindField, Detail: "Target workflow ID (for goto/retry — mutually exclusive with stepId)", InsertText: "workflowId: "},
			protocol.CompletionItem{Label: "retryAfter", Kind: protocol.CompletionItemKindField, Detail: "Seconds to wait before retrying (for retry)", InsertText: "retryAfter: "},
			protocol.CompletionItem{Label: "retryLimit", Kind: protocol.CompletionItemKindField, Detail: "Maximum number of retry attempts (for retry)", InsertText: "retryLimit: "},
			protocol.CompletionItem{Label: "criteria", Kind: protocol.CompletionItemKindField, Detail: "Conditions that must be met for this action to apply", InsertText: "criteria:\n  - condition: "},
			protocol.CompletionItem{Label: "parameters", Kind: protocol.CompletionItemKindField, Detail: "Parameters for referenced workflow (workflowId only) — 'in' MUST NOT be used (spec §5.8.8.1)", InsertText: "parameters:\n  - name: \n    value: "},
		)

	case "successCriteria":
		// Criterion Object fields (spec §5.8.11)
		items = append(items,
			protocol.CompletionItem{Label: "condition", Kind: protocol.CompletionItemKindField, Detail: "Condition expression to evaluate (REQUIRED)", InsertText: "condition: "},
			protocol.CompletionItem{Label: "context", Kind: protocol.CompletionItemKindField, Detail: "Runtime expression defining the evaluation context", InsertText: "context: "},
			protocol.CompletionItem{Label: "type", Kind: protocol.CompletionItemKindField, Detail: "Criterion type: simple, regex, jsonpath, or xpath (default: simple)", InsertText: "type: simple"},
		)

	case "root":
		// Top-level fields
		items = append(items, c.getTopLevelCompletions()...)

	default:
		// Fallback: provide both top-level and common field completions
		items = append(items, c.getTopLevelCompletions()...)
		items = append(items, c.getFieldNameCompletions(beforeCursor)...)
	}

	return items
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
