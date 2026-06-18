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
