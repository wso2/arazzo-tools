package executor

import (
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// Verifies the Phase 4 wiring: a step output declared as a Selector Object is evaluated
// (not preserved as-is) when extracting outputs from a response.
func TestExtractOutputs_SelectorObject(t *testing.T) {
	body := map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{"id": "p1", "name": "Hammer", "price": 5.0},
			map[string]interface{}{"id": "p2", "name": "Drill", "price": 80.0},
		},
	}
	step := map[string]interface{}{
		"stepId": "listProducts",
		"outputs": map[string]interface{}{
			// plain string expression (must still work)
			"firstIdString": "$response.body#/data/0/id",
			// Selector Object — JSON Pointer
			"firstIdSelector": map[string]interface{}{
				"context": "$response.body", "selector": "/data/0/id", "type": "jsonpointer",
			},
			// Selector Object — JSONPath filter
			"cheapName": map[string]interface{}{
				"context": "$response.body", "selector": "$.data[?(@.price < 10)].name", "type": "jsonpath",
			},
		},
	}
	response := map[string]interface{}{
		"status_code": 200,
		"body":        body,
		"headers":     map[string]interface{}{},
	}

	oe := NewOutputExtractor(nil)
	out := oe.ExtractOutputs(step, response, &models.ExecutionState{})

	if out["firstIdString"] != "p1" {
		t.Errorf("string expression output = %v, want p1", out["firstIdString"])
	}
	if out["firstIdSelector"] != "p1" {
		t.Errorf("jsonpointer selector output = %v, want p1", out["firstIdSelector"])
	}
	if out["cheapName"] != "Hammer" {
		t.Errorf("jsonpath selector output = %v, want Hammer", out["cheapName"])
	}
}
