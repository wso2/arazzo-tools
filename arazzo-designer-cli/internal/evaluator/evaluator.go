// Package evaluator implements the Arazzo runtime expression evaluator.
// This faithfully replicates the Python arazzo-runner's ExpressionEvaluator.
package evaluator

import (
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// EvaluateExpression evaluates an Arazzo runtime expression in the context of the current state.
// Supports: $inputs.x, $steps.x.outputs.y, $statusCode, $response.body, $response.header.x,
// JSON Pointer syntax ($response.body#/path), array access ([0]), and dot-notation navigation.
// The optional context map provides runtime values like statusCode, response, headers, body.
func EvaluateExpression(expr string, state *models.ExecutionState, sourceDescs map[string]interface{}, context map[string]interface{}) interface{} {
	if expr == "" {
		return nil
	}

	// Handle JSON pointer syntax: $response.body#/path/to/value
	if strings.Contains(expr, "#/") {
		return evaluateJSONPointer(expr, state, sourceDescs, context)
	}

	// Handle $statusCode
	if expr == "$statusCode" {
		if context != nil {
			if sc, ok := context["statusCode"]; ok {
				return sc
			}
		}
		return nil
	}

	// Handle $response.header.X
	if strings.HasPrefix(expr, "$response.header.") {
		headerName := strings.TrimPrefix(expr, "$response.header.")
		if context != nil {
			if headers, ok := context["headers"].(map[string]interface{}); ok {
				if v, ok := headers[headerName]; ok {
					return v
				}
				// Try case-insensitive
				for k, v := range headers {
					if strings.EqualFold(k, headerName) {
						return v
					}
				}
			}
			if headers, ok := context["headers"].(map[string]string); ok {
				if v, ok := headers[headerName]; ok {
					return v
				}
				for k, v := range headers {
					if strings.EqualFold(k, headerName) {
						return v
					}
				}
			}
		}
		return nil
	}

	// Handle $response.body or $response.body.path
	if strings.HasPrefix(expr, "$response.body") {
		rest := strings.TrimPrefix(expr, "$response.body")
		var body interface{}
		if context != nil {
			body = context["body"]
		}
		if rest == "" {
			return body
		}
		if strings.HasPrefix(rest, ".") {
			path := strings.TrimPrefix(rest, ".")
			return navigatePath(body, path)
		}
		return body
	}

	// Handle $response
	if expr == "$response" {
		if context != nil {
			return context["response"]
		}
		return nil
	}

	// Handle $inputs.x
	if strings.HasPrefix(expr, "$inputs.") {
		path := strings.TrimPrefix(expr, "$inputs.")
		return navigatePath(state.Inputs, path)
	}
	if expr == "$inputs" {
		return state.Inputs
	}

	// Handle $steps.stepId.outputs.x or $steps.stepId.status
	if strings.HasPrefix(expr, "$steps.") {
		return evaluateStepsExpression(expr, state)
	}

	// Handle $dependencies.workflowId.outputName
	if strings.HasPrefix(expr, "$dependencies.") {
		return evaluateDependenciesExpression(expr, state)
	}

	// --- v1.1.0 runtime-expression roots (spec §5.9) ---

	// $self -> the document's canonical URI (the $self field).
	if expr == "$self" {
		if state != nil && state.Self != "" {
			return state.Self
		}
		return nil
	}

	// $message.header.X / $message.payload[.path] / $message (AsyncAPI messages).
	if expr == "$message" || strings.HasPrefix(expr, "$message.") {
		return evaluateMessageExpression(expr, context)
	}

	// $sourceDescriptions.<name>.<reference> (spec §5.9.2 resolution priority).
	if strings.HasPrefix(expr, "$sourceDescriptions.") {
		return evaluateSourceDescriptionsExpression(expr, state, sourceDescs)
	}

	// $components.<type>.<name> (the Components Object).
	if strings.HasPrefix(expr, "$components.") {
		return evaluateComponentsExpression(expr, state)
	}

	// $workflows.<workflowId>.<field>.
	if strings.HasPrefix(expr, "$workflows.") {
		return evaluateWorkflowsExpression(expr, state)
	}

	// $url / $method of the current request (from context when available).
	if expr == "$url" {
		if context != nil {
			return context["url"]
		}
		return nil
	}
	if expr == "$method" {
		if context != nil {
			return context["method"]
		}
		return nil
	}

	return nil
}

// evaluateMessageExpression resolves $message.* runtime expressions (AsyncAPI). The message lives in
// the evaluation context under "message" as {header: {...}, payload: ...}; absent until an AsyncAPI
// runtime populates it, in which case this returns nil (the expression is simply unresolved).
func evaluateMessageExpression(expr string, context map[string]interface{}) interface{} {
	if context == nil {
		return nil
	}
	msg, ok := context["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	if expr == "$message" {
		return msg
	}
	// $message.header.<token> — single header value (case-insensitive fallback like $response.header).
	if strings.HasPrefix(expr, "$message.header.") {
		name := strings.TrimPrefix(expr, "$message.header.")
		headers, _ := msg["header"].(map[string]interface{})
		if headers == nil {
			return nil
		}
		if v, ok := headers[name]; ok {
			return v
		}
		for k, v := range headers {
			if strings.EqualFold(k, name) {
				return v
			}
		}
		return nil
	}
	// $message.payload or $message.payload.<path> (the #/pointer form is handled by evaluateJSONPointer).
	if strings.HasPrefix(expr, "$message.payload") {
		rest := strings.TrimPrefix(expr, "$message.payload")
		payload := msg["payload"]
		if rest == "" {
			return payload
		}
		if strings.HasPrefix(rest, ".") {
			return navigatePath(payload, strings.TrimPrefix(rest, "."))
		}
		return payload
	}
	return nil
}

// evaluateSourceDescriptionsExpression resolves $sourceDescriptions.<name>.<reference> per spec
// §5.9.2: first try to match <reference> against an operationId (OpenAPI/AsyncAPI source) or a
// workflowId (Arazzo source) in the referenced description; only if there is no match, treat
// <reference> as a field of the Source Description Object (e.g. url, type).
func evaluateSourceDescriptionsExpression(expr string, state *models.ExecutionState, sourceDescs map[string]interface{}) interface{} {
	rest := strings.TrimPrefix(expr, "$sourceDescriptions.")
	name := rest
	reference := ""
	if dot := strings.Index(rest, "."); dot >= 0 {
		name = rest[:dot]
		reference = rest[dot+1:]
	}
	if name == "" {
		return nil
	}

	// The loaded spec content (keyed by source name) used for operationId / workflowId matching.
	var spec map[string]interface{}
	if sourceDescs != nil {
		spec, _ = sourceDescs[name].(map[string]interface{})
	}

	// Bare "$sourceDescriptions.<name>" -> the Source Description Object, if known.
	if reference == "" {
		return sourceDescriptionObject(state, name)
	}

	// Split the reference into an id segment (matched against operationId/workflowId) and any
	// trailing navigation applied to the matched object.
	idSeg := reference
	remainder := ""
	if dot := strings.Index(reference, "."); dot >= 0 {
		idSeg = reference[:dot]
		remainder = reference[dot+1:]
	}

	// Priority 1: operationId / workflowId match in the referenced document.
	if spec != nil {
		var matched map[string]interface{}
		var found bool
		switch sourceKind(state, name, spec) {
		case "arazzo":
			matched, found = findWorkflowIDInSpec(spec, idSeg)
		case "asyncapi":
			matched, found = findAsyncAPIOperationIDInSpec(spec, idSeg)
		default: // openapi
			matched, found = findOperationIDInSpec(spec, idSeg)
		}
		if found {
			if remainder == "" {
				return matched
			}
			return navigatePath(matched, remainder)
		}
	}

	// Priority 2: a field of the Source Description Object (e.g. url, type, name).
	if sd, ok := sourceDescriptionObject(state, name).(map[string]interface{}); ok {
		return navigatePath(sd, reference)
	}
	return nil
}

// sourceDescriptionObject returns the authored Source Description Object ({name,url,type}) for a name.
func sourceDescriptionObject(state *models.ExecutionState, name string) interface{} {
	if state == nil || state.SourceDescriptionObjects == nil {
		return nil
	}
	return state.SourceDescriptionObjects[name]
}

// sourceKind reports the referenced source's kind ("openapi", "asyncapi", or "arazzo"), preferring
// the declared type on the Source Description Object and falling back to the spec's marker key.
func sourceKind(state *models.ExecutionState, name string, spec map[string]interface{}) string {
	if sd, ok := sourceDescriptionObject(state, name).(map[string]interface{}); ok {
		if t, _ := sd["type"].(string); t != "" {
			return t
		}
	}
	if spec != nil {
		if _, ok := spec["openapi"]; ok {
			return "openapi"
		}
		if _, ok := spec["asyncapi"]; ok {
			return "asyncapi"
		}
		if _, ok := spec["arazzo"]; ok {
			return "arazzo"
		}
	}
	return "openapi"
}

// findOperationIDInSpec searches an OpenAPI spec's paths for an operation with the given operationId.
func findOperationIDInSpec(spec map[string]interface{}, opID string) (map[string]interface{}, bool) {
	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	for _, pathItemRaw := range paths {
		pathItem, ok := pathItemRaw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, opRaw := range pathItem {
			op, ok := opRaw.(map[string]interface{})
			if !ok {
				continue
			}
			if id, _ := op["operationId"].(string); id == opID {
				return op, true
			}
		}
	}
	return nil, false
}

// findAsyncAPIOperationIDInSpec looks up an operation in an AsyncAPI 3.x spec, where the top-level
// `operations` field is a map keyed by operation id.
func findAsyncAPIOperationIDInSpec(spec map[string]interface{}, opID string) (map[string]interface{}, bool) {
	ops, ok := spec["operations"].(map[string]interface{})
	if !ok {
		return nil, false
	}
	op, ok := ops[opID].(map[string]interface{})
	if !ok {
		return nil, false
	}
	return op, true
}

// findWorkflowIDInSpec searches an Arazzo spec's workflows for one with the given workflowId.
func findWorkflowIDInSpec(spec map[string]interface{}, wfID string) (map[string]interface{}, bool) {
	wfs, ok := spec["workflows"].([]interface{})
	if !ok {
		return nil, false
	}
	for _, wfRaw := range wfs {
		wf, ok := wfRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := wf["workflowId"].(string); id == wfID {
			return wf, true
		}
	}
	return nil, false
}

// evaluateComponentsExpression resolves $components.<type>.<name> against the Components Object.
func evaluateComponentsExpression(expr string, state *models.ExecutionState) interface{} {
	if state == nil || state.Components == nil {
		return nil
	}
	rest := strings.TrimPrefix(expr, "$components.")
	if rest == "" {
		return state.Components
	}
	return navigatePath(state.Components, rest)
}

// evaluateWorkflowsExpression resolves $workflows.<workflowId>.<field> against the document's workflows.
func evaluateWorkflowsExpression(expr string, state *models.ExecutionState) interface{} {
	if state == nil || state.WorkflowsByID == nil {
		return nil
	}
	rest := strings.TrimPrefix(expr, "$workflows.")
	id := rest
	field := ""
	if dot := strings.Index(rest, "."); dot >= 0 {
		id = rest[:dot]
		field = rest[dot+1:]
	}
	wf, ok := state.WorkflowsByID[id]
	if !ok {
		return nil
	}
	if field == "" {
		return wf
	}
	return navigatePath(wf, field)
}

// evaluateJSONPointer handles expressions like $response.body#/path/to/value
func evaluateJSONPointer(expr string, state *models.ExecutionState, sourceDescs map[string]interface{}, context map[string]interface{}) interface{} {
	parts := strings.SplitN(expr, "#", 2)
	if len(parts) != 2 {
		return nil
	}

	containerPath := parts[0]
	pointerPath := parts[1]

	// Resolve the container value
	var container interface{}
	if strings.HasPrefix(containerPath, "$response.body") {
		if context != nil {
			container = context["body"]
		}
	} else if strings.HasPrefix(containerPath, "$steps.") {
		container = EvaluateExpression(containerPath, state, sourceDescs, context)
	} else {
		container = EvaluateExpression(containerPath, state, sourceDescs, context)
	}

	if container == nil {
		return nil
	}

	return ResolveJSONPointer(container, pointerPath)
}

// ResolveJSONPointer resolves a JSON pointer path like /path/to/value against data.
// Exported so other packages can use it (e.g., output extractor, success criteria).
func ResolveJSONPointer(data interface{}, pointerPath string) interface{} {
	if pointerPath == "" || pointerPath == "/" {
		return data
	}

	// Remove leading /
	if strings.HasPrefix(pointerPath, "/") {
		pointerPath = pointerPath[1:]
	}

	segments := strings.Split(pointerPath, "/")
	current := data

	for _, segment := range segments {
		if current == nil {
			return nil
		}

		// Decode JSON pointer escapes: ~1 -> /, ~0 -> ~
		segment = strings.ReplaceAll(segment, "~1", "/")
		segment = strings.ReplaceAll(segment, "~0", "~")

		switch v := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = v[segment]
			if !ok {
				return nil
			}
		case map[interface{}]interface{}:
			var found bool
			for k, val := range v {
				if fmt.Sprintf("%v", k) == segment {
					current = val
					found = true
					break
				}
			}
			if !found {
				return nil
			}
		case []interface{}:
			idx, err := strconv.Atoi(segment)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil
			}
			current = v[idx]
		default:
			return nil
		}
	}

	return current
}

// evaluateStepsExpression handles $steps.stepId.outputs.x, $steps.stepId.status,
// $steps.stepId.statusCode, $steps.stepId.response.body, etc.
// It navigates through state.StepsData[stepID] which stores the full step data
// (statusCode, response:{body,header,statusCode}, outputs:{...}, error).
func evaluateStepsExpression(expr string, state *models.ExecutionState) interface{} {
	rest := strings.TrimPrefix(expr, "$steps.")

	// Extract step ID (first segment)
	dotIdx := strings.Index(rest, ".")
	if dotIdx < 0 {
		// Just $steps.stepId - return all data for that step
		stepID := rest
		if data, ok := state.StepsData[stepID]; ok {
			return data
		}
		return nil
	}

	stepID := rest[:dotIdx]
	remainder := rest[dotIdx+1:]

	// Special case: $steps.stepId.status -> from StepsStatus map
	if remainder == "status" {
		if status, ok := state.StepsStatus[stepID]; ok {
			return string(status)
		}
		return nil
	}

	// Everything else navigates through StepsData
	stepData, ok := state.StepsData[stepID]
	if !ok {
		return nil
	}

	// Navigate the remainder path through the step data
	stepMap, ok := stepData.(map[string]interface{})
	if !ok {
		return nil
	}

	return navigatePath(stepMap, remainder)
}

// evaluateDependenciesExpression handles $dependencies.workflowId.outputName
func evaluateDependenciesExpression(expr string, state *models.ExecutionState) interface{} {
	rest := strings.TrimPrefix(expr, "$dependencies.")

	dotIdx := strings.Index(rest, ".")
	if dotIdx < 0 {
		wfID := rest
		if outputs, ok := state.DependencyOutputs[wfID]; ok {
			return outputs
		}
		return nil
	}

	wfID := rest[:dotIdx]
	outputPath := rest[dotIdx+1:]

	depOutputs, ok := state.DependencyOutputs[wfID]
	if !ok {
		return nil
	}

	return navigatePath(depOutputs, outputPath)
}

// navigatePath navigates a dot-separated path (with array access) on a data structure.
// e.g. "items.0.name" or "data[0].id"
func navigatePath(data interface{}, path string) interface{} {
	if data == nil || path == "" {
		return data
	}

	// Split on dots, but also handle array access like [0]
	segments := splitPath(path)
	current := data

	for _, seg := range segments {
		if current == nil {
			return nil
		}

		// Check for array index access [N]
		if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
			idxStr := seg[1 : len(seg)-1]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil
			}
			if arr, ok := current.([]interface{}); ok {
				if idx >= 0 && idx < len(arr) {
					current = arr[idx]
				} else {
					return nil
				}
			} else {
				return nil
			}
			continue
		}

		// Check for combined access like "items[0]"
		if bracketIdx := strings.Index(seg, "["); bracketIdx > 0 {
			fieldName := seg[:bracketIdx]
			arrayPart := seg[bracketIdx:]

			// Navigate to the field first
			current = getField(current, fieldName)
			if current == nil {
				return nil
			}

			// Then handle the array access
			current = navigatePath(current, arrayPart)
			continue
		}

		// Regular field access
		current = getField(current, seg)
	}

	return current
}

// splitPath splits a path like "a.b[0].c" into segments: ["a", "b", "[0]", "c"]
func splitPath(path string) []string {
	var segments []string
	current := ""

	for i := 0; i < len(path); i++ {
		ch := path[i]
		if ch == '.' {
			if current != "" {
				segments = append(segments, current)
				current = ""
			}
		} else if ch == '[' {
			if current != "" {
				segments = append(segments, current)
				current = ""
			}
			// Read until ]
			j := strings.Index(path[i:], "]")
			if j < 0 {
				current += string(ch)
			} else {
				segments = append(segments, path[i:i+j+1])
				i = i + j
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		segments = append(segments, current)
	}
	return segments
}

// getField gets a field from a map.
func getField(data interface{}, field string) interface{} {
	switch m := data.(type) {
	case map[string]interface{}:
		return m[field]
	case map[interface{}]interface{}:
		return m[field]
	default:
		return nil
	}
}

// HandleArrayAccess handles array access patterns like $steps.step1.outputs.items[0]
func HandleArrayAccess(expr string, state *models.ExecutionState) interface{} {
	// Check for array index pattern
	re := regexp.MustCompile(`^(.+)\[(\d+)\](.*)$`)
	matches := re.FindStringSubmatch(expr)
	if matches == nil {
		return nil
	}

	baseExpr := matches[1]
	idx, _ := strconv.Atoi(matches[2])
	rest := matches[3]

	value := EvaluateExpression(baseExpr, state, nil, nil)
	if value == nil {
		return nil
	}

	arr, ok := value.([]interface{})
	if !ok || idx < 0 || idx >= len(arr) {
		return nil
	}

	result := arr[idx]
	if rest != "" && strings.HasPrefix(rest, ".") {
		return navigatePath(result, rest[1:])
	}
	return result
}

// EvaluateSimpleCondition evaluates an Arazzo "simple" condition. Beyond a single comparison
// ("$statusCode == 200") it supports boolean composition: logical NOT (!), AND (&&), OR (||), and
// parentheses for grouping — e.g. "($statusCode == 200 && $response.body.ok == true) || !$error".
// Operands are runtime expressions or literals (resolveValue) and comparisons reuse compareValues.
func EvaluateSimpleCondition(condition string, state *models.ExecutionState, sourceDescs map[string]interface{}, context map[string]interface{}) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return false
	}
	p := &condParser{s: condition, state: state, src: sourceDescs, ctx: context}
	result := p.parseOr()
	p.skipWS()
	// A well-formed condition is fully consumed with balanced parentheses. If not (trailing/unparsed
	// input, or a missing closing paren), the condition is malformed — fail safe to false rather than
	// returning a partial-parse result that could wrongly read as true.
	if p.bad || p.pos != len(p.s) {
		log.Printf("Warning: malformed condition %q (unparsed input or unbalanced parentheses); treating as false", condition)
		return false
	}
	return result
}

// condParser is a small recursive-descent evaluator for boolean conditions. Precedence (lowest to
// highest): || , && , unary ! , then a primary which is either a parenthesised sub-condition or a
// single comparison. Quotes are respected so operators inside string literals are not treated as
// syntax.
type condParser struct {
	s     string
	pos   int
	bad   bool // set when the condition is malformed (e.g. a missing closing parenthesis)
	state *models.ExecutionState
	src   map[string]interface{}
	ctx   map[string]interface{}
}

func (p *condParser) parseOr() bool {
	v := p.parseAnd()
	for {
		p.skipWS()
		if p.hasPrefix("||") {
			p.pos += 2
			r := p.parseAnd()
			v = v || r
			continue
		}
		break
	}
	return v
}

func (p *condParser) parseAnd() bool {
	v := p.parseUnary()
	for {
		p.skipWS()
		if p.hasPrefix("&&") {
			p.pos += 2
			r := p.parseUnary()
			v = v && r
			continue
		}
		break
	}
	return v
}

func (p *condParser) parseUnary() bool {
	p.skipWS()
	// A leading '!' is logical NOT — but not '!=', which is a comparison operator.
	if p.pos < len(p.s) && p.s[p.pos] == '!' && !(p.pos+1 < len(p.s) && p.s[p.pos+1] == '=') {
		p.pos++
		return !p.parseUnary()
	}
	return p.parsePrimary()
}

func (p *condParser) parsePrimary() bool {
	p.skipWS()
	if p.pos < len(p.s) && p.s[p.pos] == '(' {
		p.pos++
		v := p.parseOr()
		p.skipWS()
		if p.pos < len(p.s) && p.s[p.pos] == ')' {
			p.pos++
		} else {
			p.bad = true // opened '(' without a matching ')'
		}
		return v
	}
	return p.evalComparison(p.readComparisonChunk())
}

// readComparisonChunk consumes a single comparison operand-and-operator run, stopping at a top-level
// boolean operator (&&, ||) or a closing paren, while ignoring those inside quoted strings.
func (p *condParser) readComparisonChunk() string {
	start := p.pos
	var quote byte
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			p.pos++
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
		case c == ')':
			return strings.TrimSpace(p.s[start:p.pos])
		case c == '&' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '&':
			return strings.TrimSpace(p.s[start:p.pos])
		case c == '|' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '|':
			return strings.TrimSpace(p.s[start:p.pos])
		}
		p.pos++
	}
	return strings.TrimSpace(p.s[start:p.pos])
}

// evalComparison evaluates a single comparison chunk: "left OP right" for a relational operator, or
// a bare expression treated as truthy when no operator is present.
func (p *condParser) evalComparison(chunk string) bool {
	if chunk == "" {
		return false
	}
	// Two-character operators first so '>=' / '<=' / '==' / '!=' win over '>' / '<'.
	for _, op := range []string{"==", "!=", ">=", "<="} {
		if idx := topLevelIndex(chunk, op); idx >= 0 {
			return p.compareSides(chunk[:idx], chunk[idx+len(op):], op)
		}
	}
	for _, op := range []string{">", "<"} {
		if idx := topLevelIndex(chunk, op); idx >= 0 {
			return p.compareSides(chunk[:idx], chunk[idx+len(op):], op)
		}
	}
	return isTruthy(resolveValue(strings.TrimSpace(chunk), p.state, p.src, p.ctx))
}

func (p *condParser) compareSides(left, right, op string) bool {
	l := resolveValue(strings.TrimSpace(left), p.state, p.src, p.ctx)
	r := resolveValue(strings.TrimSpace(right), p.state, p.src, p.ctx)
	return compareValues(l, r, op)
}

func (p *condParser) skipWS() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t' || p.s[p.pos] == '\n' || p.s[p.pos] == '\r') {
		p.pos++
	}
}

func (p *condParser) hasPrefix(s string) bool {
	return strings.HasPrefix(p.s[p.pos:], s)
}

// topLevelIndex finds the first index of op in s that lies outside any quoted string, or -1.
func topLevelIndex(s, op string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if strings.HasPrefix(s[i:], op) {
			return i
		}
	}
	return -1
}

// resolveValue resolves a value from an expression or literal.
func resolveValue(expr string, state *models.ExecutionState, sourceDescs map[string]interface{}, context map[string]interface{}) interface{} {
	expr = strings.TrimSpace(expr)

	// Expression starting with $
	if strings.HasPrefix(expr, "$") {
		return EvaluateExpression(expr, state, sourceDescs, context)
	}

	// String literal
	if (strings.HasPrefix(expr, "'") && strings.HasSuffix(expr, "'")) ||
		(strings.HasPrefix(expr, "\"") && strings.HasSuffix(expr, "\"")) {
		return expr[1 : len(expr)-1]
	}

	// Boolean
	if expr == "true" {
		return true
	}
	if expr == "false" {
		return false
	}

	// Null
	if expr == "null" || expr == "None" {
		return nil
	}

	// Number (int or float)
	if i, err := strconv.ParseInt(expr, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(expr, 64); err == nil {
		return f
	}

	// Return as string
	return expr
}

// compareValues compares two values with the given operator.
func compareValues(left, right interface{}, op string) bool {
	// Normalize numeric types for comparison
	leftNum, leftIsNum := toFloat64(left)
	rightNum, rightIsNum := toFloat64(right)

	switch op {
	case "==":
		if leftIsNum && rightIsNum {
			return leftNum == rightNum
		}
		return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right)
	case "!=":
		if leftIsNum && rightIsNum {
			return leftNum != rightNum
		}
		return fmt.Sprintf("%v", left) != fmt.Sprintf("%v", right)
	case ">":
		if leftIsNum && rightIsNum {
			return leftNum > rightNum
		}
		return fmt.Sprintf("%v", left) > fmt.Sprintf("%v", right)
	case "<":
		if leftIsNum && rightIsNum {
			return leftNum < rightNum
		}
		return fmt.Sprintf("%v", left) < fmt.Sprintf("%v", right)
	case ">=":
		if leftIsNum && rightIsNum {
			return leftNum >= rightNum
		}
		return fmt.Sprintf("%v", left) >= fmt.Sprintf("%v", right)
	case "<=":
		if leftIsNum && rightIsNum {
			return leftNum <= rightNum
		}
		return fmt.Sprintf("%v", left) <= fmt.Sprintf("%v", right)
	}
	return false
}

// toFloat64 tries to convert an interface to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// isTruthy checks if a value is truthy.
func isTruthy(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case float64:
		return val != 0
	case int:
		return val != 0
	case int64:
		return val != 0
	case []interface{}:
		return len(val) > 0
	case map[string]interface{}:
		return len(val) > 0
	default:
		return true
	}
}

// ProcessObjectExpressions recursively resolves expressions in a map.
// This replicates Python's ExpressionEvaluator.process_object_expressions.
func ProcessObjectExpressions(obj map[string]interface{}, state *models.ExecutionState, sourceDescs map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for key, value := range obj {
		result[key] = processValue(value, state, sourceDescs)
	}
	return result
}

// ProcessArrayExpressions recursively resolves expressions in a slice.
func ProcessArrayExpressions(arr []interface{}, state *models.ExecutionState, sourceDescs map[string]interface{}) []interface{} {
	result := make([]interface{}, len(arr))
	for i, value := range arr {
		result[i] = processValue(value, state, sourceDescs)
	}
	return result
}

// processValue handles resolving expressions in a single value, which may be string, map, or slice.
func processValue(value interface{}, state *models.ExecutionState, sourceDescs map[string]interface{}) interface{} {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "$") {
			evaluated := EvaluateExpression(v, state, sourceDescs, nil)
			if evaluated != nil {
				return evaluated
			}
			return v
		}
		// Handle template expressions like "Bearer {$inputs.token}"
		if strings.Contains(v, "{$") {
			return resolveTemplateString(v, state, sourceDescs)
		}
		return v
	case map[string]interface{}:
		// A v1.1.0 Selector Object is evaluated as a whole; a plain object is recursed into.
		if IsSelectorObject(v) {
			result, err := EvaluateSelectorObject(v, state, sourceDescs, nil)
			if err != nil {
				// Fail safe: don't leak the raw {context,selector,type} descriptor downstream.
				log.Printf("Warning: selector object evaluation failed: %v", err)
				return nil
			}
			return result
		}
		return ProcessObjectExpressions(v, state, sourceDescs)
	case []interface{}:
		return ProcessArrayExpressions(v, state, sourceDescs)
	default:
		return v
	}
}

// resolveTemplateString replaces {$...} placeholders in a string with their evaluated values.
// Primitives embed as their text form; objects/arrays embed as JSON (so an embedded structure is
// serialized consistently rather than as Go's map[...] formatting). An expression that does not
// resolve is left in place, with a context-aware warning.
func resolveTemplateString(template string, state *models.ExecutionState, sourceDescs map[string]interface{}) string {
	re := regexp.MustCompile(`\{(\$[^}]+)\}`)
	return re.ReplaceAllStringFunc(template, func(match string) string {
		// Extract the expression (remove { and })
		expr := match[1 : len(match)-1]
		val := EvaluateExpression(expr, state, sourceDescs, nil)
		if val == nil {
			log.Printf("Warning: template expression %q in %q evaluated to nil; leaving the placeholder unresolved", expr, template)
			return match
		}
		return embedValue(val)
	})
}

// embedValue renders a value for embedding inside a string: strings as-is, objects/arrays as JSON,
// and other primitives via their default formatting.
func embedValue(val interface{}) string {
	switch v := val.(type) {
	case string:
		return v
	case map[string]interface{}, []interface{}:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
