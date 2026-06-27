package executor

import (
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/models"
)

func selectorTestState() *models.ExecutionState {
	return &models.ExecutionState{
		StepsData: map[string]interface{}{
			"listProducts": map[string]interface{}{
				"outputs": map[string]interface{}{
					"body": map[string]interface{}{
						"data": []interface{}{
							map[string]interface{}{"id": "p1", "name": "Hammer", "price": 5.0},
						},
					},
				},
			},
		},
	}
}

// Parameter value declared as a Selector Object is evaluated.
func TestPrepareParameters_SelectorObject(t *testing.T) {
	step := map[string]interface{}{
		"parameters": []interface{}{
			map[string]interface{}{
				"name": "productId", "in": "path",
				"value": map[string]interface{}{
					"context": "$steps.listProducts.outputs.body", "selector": "/data/0/id", "type": "jsonpointer",
				},
			},
		},
	}
	pp := NewParameterProcessor(nil)
	params := pp.PrepareParameters(step, selectorTestState())
	path, _ := params["path"].(map[string]interface{})
	if path == nil || path["productId"] != "p1" {
		t.Errorf("path.productId = %v, want p1 (params=%v)", path["productId"], params)
	}
}

// A replacement that uses a JSONPath target (targetSelectorType: jsonpath) instead of a JSON Pointer.
func TestApplyReplacements_JSONPathTarget(t *testing.T) {
	requestBody := map[string]interface{}{
		"contentType": "application/json",
		"payload":     map[string]interface{}{"product_id": "PLACEHOLDER", "quantity": 1.0},
		"replacements": []interface{}{
			map[string]interface{}{
				"target":             "$.product_id",
				"targetSelectorType": "jsonpath",
				"value":              "p1",
			},
		},
	}
	pp := NewParameterProcessor(nil)
	out := pp.PrepareRequestBody(requestBody, &models.ExecutionState{})
	payload, _ := out["payload"].(map[string]interface{})
	if payload == nil || payload["product_id"] != "p1" {
		t.Errorf("product_id = %v, want p1 (JSONPath replacement target)", payload["product_id"])
	}
	if payload["quantity"] != 1.0 {
		t.Errorf("quantity should be untouched, got %v", payload["quantity"])
	}
}

// A default (JSON Pointer) replacement target that indexes into an array, e.g. /items/0/product_id.
// This is the write-side counterpart of evaluator.ResolveJSONPointer's array support.
func TestApplyReplacements_JSONPointerArrayIndex(t *testing.T) {
	requestBody := map[string]interface{}{
		"contentType": "application/json",
		"payload": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"product_id": "PLACEHOLDER", "quantity": 1.0},
				map[string]interface{}{"product_id": "KEEP", "quantity": 2.0},
			},
		},
		"replacements": []interface{}{
			// array index in an intermediate segment ("0") then an object key
			map[string]interface{}{"target": "/items/0/product_id", "value": "p1"},
		},
	}
	pp := NewParameterProcessor(nil)
	out := pp.PrepareRequestBody(requestBody, &models.ExecutionState{})
	payload, _ := out["payload"].(map[string]interface{})
	items, _ := payload["items"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("items malformed: %v", payload["items"])
	}
	first, _ := items[0].(map[string]interface{})
	second, _ := items[1].(map[string]interface{})
	if first["product_id"] != "p1" {
		t.Errorf("items[0].product_id = %v, want p1 (array-index JSON Pointer target)", first["product_id"])
	}
	if second["product_id"] != "KEEP" {
		t.Errorf("items[1] should be untouched, got %v", second["product_id"])
	}
}

// An array index as the FINAL segment (/tags/1) and an out-of-range index left as a safe no-op.
func TestApplyReplacements_JSONPointerArrayEdgeCases(t *testing.T) {
	requestBody := map[string]interface{}{
		"contentType": "application/json",
		"payload": map[string]interface{}{
			"tags": []interface{}{"a", "b", "c"},
		},
		"replacements": []interface{}{
			map[string]interface{}{"target": "/tags/1", "value": "B"},      // valid final-index set
			map[string]interface{}{"target": "/tags/9", "value": "nope"},   // out of range -> warn + no-op
		},
	}
	pp := NewParameterProcessor(nil)
	out := pp.PrepareRequestBody(requestBody, &models.ExecutionState{})
	payload, _ := out["payload"].(map[string]interface{})
	tags, _ := payload["tags"].([]interface{})
	want := []interface{}{"a", "B", "c"}
	if len(tags) != 3 || tags[0] != want[0] || tags[1] != want[1] || tags[2] != want[2] {
		t.Errorf("tags = %v, want %v (in-range set applied, out-of-range ignored)", tags, want)
	}
}

// A nested Selector Object inside a payload, and a replacement whose value is a Selector Object.
func TestPrepareRequestBody_SelectorObjects(t *testing.T) {
	requestBody := map[string]interface{}{
		"contentType": "application/json",
		"payload": map[string]interface{}{
			"productId": map[string]interface{}{
				"context": "$steps.listProducts.outputs.body", "selector": "$.data[0].id", "type": "jsonpath",
			},
			"quantity": 1,
		},
		"replacements": []interface{}{
			map[string]interface{}{
				"target": "/quantity",
				"value": map[string]interface{}{
					"context": "$steps.listProducts.outputs.body", "selector": "/data/0/price", "type": "jsonpointer",
				},
			},
		},
	}
	pp := NewParameterProcessor(nil)
	out := pp.PrepareRequestBody(requestBody, selectorTestState())
	payload, _ := out["payload"].(map[string]interface{})
	if payload == nil {
		t.Fatalf("payload missing: %v", out)
	}
	if payload["productId"] != "p1" {
		t.Errorf("payload.productId = %v, want p1 (nested JSONPath selector)", payload["productId"])
	}
	if payload["quantity"] != 5.0 {
		t.Errorf("payload.quantity = %v, want 5 (replacement selector)", payload["quantity"])
	}
}
