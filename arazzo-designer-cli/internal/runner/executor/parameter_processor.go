// parameter_processor.go replicates the Python arazzo-runner's ParameterProcessor class.
package executor

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/wso2/arazzo-designer-cli/internal/evaluator"
	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// ParameterProcessor processes parameters and request bodies for API operations.
type ParameterProcessor struct {
	SourceDescriptions map[string]interface{}
}

// NewParameterProcessor creates a new ParameterProcessor.
func NewParameterProcessor(sourceDescs map[string]interface{}) *ParameterProcessor {
	return &ParameterProcessor{SourceDescriptions: sourceDescs}
}

// PrepareParameters prepares parameters for an operation execution from a step definition.
// Returns a map with keys: "path", "query", "header", "cookie" (each a map[string]interface{}).
func (pp *ParameterProcessor) PrepareParameters(step map[string]interface{}, state *models.ExecutionState) map[string]interface{} {
	parameters := make(map[string]interface{})

	params := toSlice(step["parameters"])
	for _, paramRaw := range params {
		param := toMap(paramRaw)
		if param == nil {
			continue
		}

		name, _ := param["name"].(string)
		location, _ := param["in"].(string)
		value := param["value"]

		// Process the value to resolve any expressions
		value = pp.resolveParameterValue(value, state)

		// Log warning if value still contains unresolved expression
		if s, ok := value.(string); ok && strings.Contains(s, "$") {
			if strings.HasPrefix(s, "$") || strings.Contains(s, "{$") {
				log.Printf("Warning: Parameter '%s' value '%s' still contains expression syntax after evaluation", name, s)
			}
		}

		// Organize parameters by location
		switch location {
		case "path":
			ensureParamMap(parameters, "path")[name] = value
		case "query":
			ensureParamMap(parameters, "query")[name] = value
		case "header":
			ensureParamMap(parameters, "header")[name] = value
		case "cookie":
			ensureParamMap(parameters, "cookie")[name] = value
		default:
			// For workflow inputs (no location specified)
			parameters[name] = value
		}
	}

	return parameters
}

// PrepareRequestBody prepares the request body for an operation execution.
func (pp *ParameterProcessor) PrepareRequestBody(requestBody map[string]interface{}, state *models.ExecutionState) map[string]interface{} {
	if requestBody == nil {
		return nil
	}

	contentType, _ := requestBody["contentType"].(string)
	payload := requestBody["payload"]

	// Evaluate the entire payload to resolve expressions.
	// Nested Selector Objects inside a map/array are handled by Process*Expressions; a payload
	// that is itself a Selector Object is evaluated here.
	if evaluator.IsSelectorObject(payload) {
		selMap, _ := payload.(map[string]interface{})
		if value, err := evaluator.EvaluateSelectorObject(selMap, state, pp.SourceDescriptions, nil); err == nil {
			payload = value
		} else {
			// Fail safe: don't ship the raw {context,selector,type} descriptor as the body.
			log.Printf("Warning: payload selector failed: %v", err)
			payload = nil
		}
	} else if m, ok := payload.(map[string]interface{}); ok {
		payload = evaluator.ProcessObjectExpressions(m, state, pp.SourceDescriptions)
	} else if arr, ok := payload.([]interface{}); ok {
		payload = evaluator.ProcessArrayExpressions(arr, state, pp.SourceDescriptions)
	} else if s, ok := payload.(string); ok {
		payload = pp.resolveStringPayload(s, state, contentType)
	}

	// Handle replacements
	replacements := toSlice(requestBody["replacements"])
	if len(replacements) > 0 {
		payload = applyReplacements(payload, replacements, state, pp.SourceDescriptions)
	}

	return map[string]interface{}{
		"contentType": contentType,
		"payload":     payload,
	}
}

// resolveParameterValue resolves expressions in a parameter value.
func (pp *ParameterProcessor) resolveParameterValue(value interface{}, state *models.ExecutionState) interface{} {
	switch v := value.(type) {
	case string:
		if strings.HasPrefix(v, "$") {
			// Try array access first
			arrayValue := evaluator.HandleArrayAccess(v, state)
			if arrayValue != nil {
				return arrayValue
			}
			// Standard expression evaluation
			result := evaluator.EvaluateExpression(v, state, pp.SourceDescriptions, nil)
			if result != nil {
				return result
			}
			return v
		}

		// Check for embedded expressions like "Bearer $inputs.token"
		if strings.Contains(v, " $") {
			return pp.resolveEmbeddedExpressions(v, state)
		}

		// Template expressions like "{$inputs.value}"
		if strings.Contains(v, "{$") {
			re := regexp.MustCompile(`\{(\$[^}]+)\}`)
			return re.ReplaceAllStringFunc(v, func(match string) string {
				expr := match[1 : len(match)-1]
				evalValue := evaluator.EvaluateExpression(expr, state, pp.SourceDescriptions, nil)
				if evalValue == nil {
					return ""
				}
				return fmt.Sprintf("%v", evalValue)
			})
		}

		// Check for inline expression references
		exprRe := regexp.MustCompile(`\$inputs\.\w+|\$steps\.\w+`)
		if exprRe.MatchString(v) {
			return exprRe.ReplaceAllStringFunc(v, func(match string) string {
				evalVal := evaluator.EvaluateExpression(match, state, pp.SourceDescriptions, nil)
				if evalVal == nil {
					log.Printf("Warning: Embedded expression %s evaluated to nil", match)
					return match
				}
				return fmt.Sprintf("%v", evalVal)
			})
		}

		return v

	case map[string]interface{}:
		// A v1.1.0 Selector Object is evaluated as a whole; a plain object is recursed into.
		if evaluator.IsSelectorObject(v) {
			if value, err := evaluator.EvaluateSelectorObject(v, state, pp.SourceDescriptions, nil); err == nil {
				return value
			} else {
				// Fail safe: don't leak the raw {context,selector,type} descriptor as the value.
				log.Printf("Warning: parameter selector failed: %v", err)
				return nil
			}
		}
		return evaluator.ProcessObjectExpressions(v, state, pp.SourceDescriptions)

	case []interface{}:
		return evaluator.ProcessArrayExpressions(v, state, pp.SourceDescriptions)

	default:
		return v
	}
}

// resolveEmbeddedExpressions resolves "Bearer $inputs.token" style expressions.
func (pp *ParameterProcessor) resolveEmbeddedExpressions(value string, state *models.ExecutionState) interface{} {
	parts := strings.SplitN(value, " $", 2)
	if len(parts) != 2 {
		return value
	}

	prefix := parts[0] + " "
	expr := "$" + parts[1]

	exprValue := evaluator.EvaluateExpression(expr, state, pp.SourceDescriptions, nil)
	if exprValue != nil {
		return prefix + fmt.Sprintf("%v", exprValue)
	}

	log.Printf("Warning: Expression %s evaluated to nil - keeping original value: %s", expr, value)
	return value
}

// resolveStringPayload resolves expressions in a string payload.
func (pp *ParameterProcessor) resolveStringPayload(payload string, state *models.ExecutionState, contentType string) interface{} {
	// Direct expression payload
	if strings.HasPrefix(payload, "$") {
		result := evaluator.EvaluateExpression(payload, state, pp.SourceDescriptions, nil)
		if result != nil {
			return result
		}
		return payload
	}

	// Template expressions
	if strings.Contains(payload, "{") && strings.Contains(payload, "}") {
		re := regexp.MustCompile(`\{(\$[^}]+)\}`)
		resolved := re.ReplaceAllStringFunc(payload, func(match string) string {
			expr := match[1 : len(match)-1]
			if !strings.HasPrefix(expr, "$") {
				return match
			}
			val := evaluator.EvaluateExpression(expr, state, pp.SourceDescriptions, nil)
			if val == nil {
				return "null"
			}
			return fmt.Sprintf("%v", val)
		})
		return resolved
	}

	return payload
}

// applyReplacements applies JSON Pointer replacements to a payload.
func applyReplacements(payload interface{}, replacements []interface{}, state *models.ExecutionState, sourceDescs map[string]interface{}) interface{} {
	for _, repRaw := range replacements {
		rep := toMap(repRaw)
		if rep == nil {
			continue
		}

		target, _ := rep["target"].(string)
		value := rep["value"]

		// Resolve the replacement value: Selector Object, runtime expression, or literal.
		if evaluator.IsSelectorObject(value) {
			selMap, _ := value.(map[string]interface{})
			resolved, err := evaluator.EvaluateSelectorObject(selMap, state, sourceDescs, nil)
			if err != nil {
				// Fail safe: skip the replacement rather than injecting the raw selector descriptor.
				log.Printf("Warning: replacement selector failed (target %q): %v; skipping replacement", target, err)
				continue
			}
			value = resolved
		} else if s, ok := value.(string); ok && strings.HasPrefix(s, "$") {
			value = evaluator.EvaluateExpression(s, state, sourceDescs, nil)
		}

		// Determine the target dialect from targetSelectorType (spec §5.8.15).
		// Default when omitted: JSON Pointer (the spec default for JSON payloads).
		dialect := "jsonpointer"
		if tst := rep["targetSelectorType"]; tst != nil {
			d, _, err := evaluator.ResolveExpressionType(tst)
			if err != nil {
				log.Printf("Warning: replacement 'targetSelectorType' invalid: %v", err)
				continue
			}
			dialect = d
		}

		// Apply the replacement at the target, interpreting it per the dialect.
		switch dialect {
		case "jsonpointer":
			if strings.HasPrefix(target, "/") {
				payload = setJSONPointer(payload, target, value)
			} else {
				log.Printf("Warning: JSON Pointer replacement target %q must start with '/'", target)
			}
		case "jsonpath":
			if updated, err := evaluator.SetJSONPath(payload, target, value); err == nil {
				payload = updated
			} else {
				log.Printf("Warning: JSONPath replacement target failed: %v", err)
			}
		case "xpath":
			log.Printf("Warning: XPath replacement targets are not yet supported (target %q)", target)
		}
	}
	return payload
}

// setJSONPointer sets a value at a JSON Pointer path (RFC 6901) within data, descending through
// both JSON objects (map[string]interface{}) and JSON arrays ([]interface{}). It mirrors the read
// side (evaluator.ResolveJSONPointer) so a target that can be READ can also be PATCHED — e.g.
// /data/0/price, where "0" indexes into an array.
//
// The edit is made in place: Go maps and slices are reference types, so mutating a nested container
// is visible through the returned data without rebuilding its parents. Unresolvable targets (a
// missing object key, an array index out of range, a numeric segment against a non-array, or trying
// to descend into a scalar) are left unchanged with a warning rather than a silent no-op.
func setJSONPointer(data interface{}, pointer string, value interface{}) interface{} {
	parts := strings.Split(pointer, "/")
	if len(parts) < 2 {
		return data
	}
	parts = parts[1:] // Skip the leading empty element (the "" before the first '/').

	current := data
	for i, part := range parts {
		// Decode JSON Pointer escapes: ~1 -> /, ~0 -> ~ (RFC 6901 §3).
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")

		last := i == len(parts)-1

		switch node := current.(type) {
		case map[string]interface{}:
			if last {
				node[part] = value
				return data
			}
			next, ok := node[part]
			if !ok {
				log.Printf("Warning: JSON Pointer target %q not applied: missing object key %q", pointer, part)
				return data
			}
			current = next
		case []interface{}:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(node) {
				log.Printf("Warning: JSON Pointer target %q not applied: invalid array index %q (array length %d)", pointer, part, len(node))
				return data
			}
			if last {
				node[idx] = value
				return data
			}
			current = node[idx]
		default:
			log.Printf("Warning: JSON Pointer target %q not applied: cannot descend into segment %q (node is neither object nor array)", pointer, part)
			return data
		}
	}

	return data
}

// ensureParamMap ensures a parameter location map exists and returns it.
func ensureParamMap(parameters map[string]interface{}, key string) map[string]interface{} {
	if m, ok := parameters[key].(map[string]interface{}); ok {
		return m
	}
	m := make(map[string]interface{})
	parameters[key] = m
	return m
}
