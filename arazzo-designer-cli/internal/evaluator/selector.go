// selector.go implements the Arazzo v1.1.0 Selector Object (spec §5.8.13) and the
// Expression Type Object (spec §5.8.12). A Selector Object extracts a value from
// structured data (JSON/XML) using a JSON Pointer, JSONPath, or XPath selector.
//
// This is the shared selector-evaluation service used by output extraction, parameter
// values, request-body payloads, and payload replacements. XPath is not yet supported
// and returns a clear error.
package evaluator

import (
	"fmt"

	"github.com/ohler55/ojg/jp"
	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// Default expression versions applied when a Selector Object's `type` is given as a bare
// string (or when an Expression Type Object omits `version`). Per spec §5.8.12.
const (
	defaultJSONPathVersion    = "rfc9535"
	defaultXPathVersion       = "xpath-31"
	defaultJSONPointerVersion = "rfc6901"
)

// allowedVersions lists the spec-permitted versions for each selector dialect (§5.8.12).
var allowedVersions = map[string]map[string]bool{
	"jsonpath":    {"rfc9535": true, "draft-goessner-dispatch-jsonpath-00": true},
	"xpath":       {"xpath-31": true, "xpath-30": true, "xpath-20": true, "xpath-10": true},
	"jsonpointer": {"rfc6901": true},
}

// IsSelectorObject reports whether v is a v1.1.0 Selector Object: a map carrying the
// three required fields context, selector, and type (spec §5.8.13).
func IsSelectorObject(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok {
		return false
	}
	_, hasContext := m["context"]
	_, hasSelector := m["selector"]
	_, hasType := m["type"]
	return hasContext && hasSelector && hasType
}

// EvaluateSelectorObject evaluates a Selector Object: it resolves the `context` runtime
// expression to obtain structured data, then applies the `selector` using the dialect named
// by `type`. The context map carries response data ($response.body etc.) and may be nil when
// no response is in scope (e.g. evaluating parameter/payload selectors before a request).
func EvaluateSelectorObject(sel map[string]interface{}, state *models.ExecutionState, sourceDescs, context map[string]interface{}) (interface{}, error) {
	contextExpr, _ := sel["context"].(string)
	selector, _ := sel["selector"].(string)

	dialect, _, err := resolveExpressionType(sel["type"])
	if err != nil {
		return nil, err
	}
	if contextExpr == "" {
		return nil, fmt.Errorf("selector object is missing a 'context' expression")
	}

	// Resolve the context expression to the data the selector runs against.
	data := EvaluateExpression(contextExpr, state, sourceDescs, context)
	if data == nil {
		return nil, fmt.Errorf("selector context %q resolved to nil", contextExpr)
	}

	switch dialect {
	case "jsonpointer":
		return ResolveJSONPointer(data, selector), nil
	case "jsonpath":
		return EvaluateJSONPathValue(data, selector)
	case "xpath":
		return nil, fmt.Errorf("XPath selectors are not yet supported (selector %q)", selector)
	default:
		return nil, fmt.Errorf("unsupported selector type %q", dialect)
	}
}

// resolveExpressionType interprets a Selector Object / criterion `type`, which may be a bare
// string (e.g. "jsonpath") or an Expression Type Object map ({type, version}). It returns the
// dialect and the resolved version, applying defaults when the version is omitted and
// rejecting unknown dialects or unsupported versions (spec §5.8.12).
func resolveExpressionType(typeField interface{}) (dialect string, version string, err error) {
	switch t := typeField.(type) {
	case string:
		dialect = t
	case map[string]interface{}:
		dialect, _ = t["type"].(string)
		version, _ = t["version"].(string)
	default:
		return "", "", fmt.Errorf("selector 'type' must be a string or an Expression Type Object")
	}

	versions, known := allowedVersions[dialect]
	if !known {
		return "", "", fmt.Errorf("unsupported selector type %q (must be jsonpath, xpath, or jsonpointer)", dialect)
	}
	if version == "" {
		switch dialect {
		case "jsonpath":
			version = defaultJSONPathVersion
		case "xpath":
			version = defaultXPathVersion
		case "jsonpointer":
			version = defaultJSONPointerVersion
		}
	}
	if !versions[version] {
		return "", "", fmt.Errorf("unsupported %s version %q", dialect, version)
	}
	return dialect, version, nil
}

// EvaluateJSONPathValue runs a JSONPath (RFC 9535) selector against data and returns the
// extracted value: nil for no match, the single value for one match, or a slice for many.
// (This complements EvaluateJSONPathCriterion, which only reports presence as a bool.)
func EvaluateJSONPathValue(data interface{}, selector string) (interface{}, error) {
	expr, err := jp.ParseString(selector)
	if err != nil {
		return nil, fmt.Errorf("invalid JSONPath %q: %w", selector, err)
	}
	normalized, err := normalizeForOJG(data)
	if err != nil {
		return nil, err
	}
	results := expr.Get(normalized)
	switch len(results) {
	case 0:
		return nil, nil
	case 1:
		return results[0], nil
	default:
		return results, nil
	}
}
