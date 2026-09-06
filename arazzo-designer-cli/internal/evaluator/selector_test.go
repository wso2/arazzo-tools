package evaluator

import (
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/models"
)

func sampleBody() map[string]interface{} {
	return map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{"id": "p1", "name": "Hammer", "price": 5.0},
			map[string]interface{}{"id": "p2", "name": "Drill", "price": 80.0},
		},
	}
}

func TestIsSelectorObject(t *testing.T) {
	yes := map[string]interface{}{"context": "$response.body", "selector": "/x", "type": "jsonpointer"}
	if !IsSelectorObject(yes) {
		t.Error("expected a {context,selector,type} map to be a Selector Object")
	}
	for _, no := range []interface{}{
		"a string",
		map[string]interface{}{"context": "x", "selector": "y"}, // missing type
		map[string]interface{}{"foo": "bar"},
		42,
		nil,
	} {
		if IsSelectorObject(no) {
			t.Errorf("did not expect %v to be a Selector Object", no)
		}
	}
}

func TestResolveExpressionType(t *testing.T) {
	cases := []struct {
		name        string
		in          interface{}
		wantDialect string
		wantVersion string
		wantErr     bool
	}{
		{"string jsonpath default", "jsonpath", "jsonpath", "rfc9535", false},
		{"string jsonpointer default", "jsonpointer", "jsonpointer", "rfc6901", false},
		{"string xpath default", "xpath", "xpath", "xpath-31", false},
		{"object explicit", map[string]interface{}{"type": "jsonpath", "version": "rfc9535"}, "jsonpath", "rfc9535", false},
		{"object missing version -> error (spec §5.8.12.1)", map[string]interface{}{"type": "jsonpath"}, "", "", true},
		{"object bad version", map[string]interface{}{"type": "jsonpath", "version": "nope"}, "", "", true},
		{"unknown dialect string", "yaml", "", "", true},
		{"unknown dialect object", map[string]interface{}{"type": "yaml"}, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, v, err := ResolveExpressionType(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got dialect=%q version=%q", d, v)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d != tc.wantDialect || v != tc.wantVersion {
				t.Errorf("got (%q,%q), want (%q,%q)", d, v, tc.wantDialect, tc.wantVersion)
			}
		})
	}
}

func TestEvaluateJSONPathValue(t *testing.T) {
	body := sampleBody()
	// single match
	if v, err := EvaluateJSONPathValue(body, "$.data[0].id"); err != nil || v != "p1" {
		t.Errorf("single: got %v, %v; want p1", v, err)
	}
	// filter → single
	if v, err := EvaluateJSONPathValue(body, "$.data[?(@.price < 10)].name"); err != nil || v != "Hammer" {
		t.Errorf("filter: got %v, %v; want Hammer", v, err)
	}
	// multiple → slice
	v, err := EvaluateJSONPathValue(body, "$.data[*].id")
	if err != nil {
		t.Fatalf("multiple: %v", err)
	}
	if arr, ok := v.([]interface{}); !ok || len(arr) != 2 {
		t.Errorf("multiple: expected 2-element slice, got %v", v)
	}
	// no match → nil
	if v, err := EvaluateJSONPathValue(body, "$.data[99].id"); err != nil || v != nil {
		t.Errorf("no match: got %v, %v; want nil", v, err)
	}
}

func TestSetJSONPath(t *testing.T) {
	// set a flat field
	data := map[string]interface{}{"product_id": "OLD", "quantity": 1.0}
	out, err := SetJSONPath(data, "$.product_id", "NEW")
	if err != nil {
		t.Fatalf("set flat: %v", err)
	}
	m := out.(map[string]interface{})
	if m["product_id"] != "NEW" {
		t.Errorf("product_id = %v, want NEW", m["product_id"])
	}
	if m["quantity"] != 1.0 {
		t.Errorf("quantity should be untouched, got %v", m["quantity"])
	}

	// set via a filter into an array (something a JSON Pointer can't do)
	data2 := map[string]interface{}{"items": []interface{}{
		map[string]interface{}{"sku": "A", "qty": 1.0},
		map[string]interface{}{"sku": "B", "qty": 1.0},
	}}
	out2, err := SetJSONPath(data2, "$.items[?(@.sku == 'B')].qty", 5)
	if err != nil {
		t.Fatalf("set filter: %v", err)
	}
	items := out2.(map[string]interface{})["items"].([]interface{})
	if got, _ := toFloat64(items[1].(map[string]interface{})["qty"]); got != 5 {
		t.Errorf("items[1].qty = %v, want 5", items[1].(map[string]interface{})["qty"])
	}
	if items[0].(map[string]interface{})["qty"] != 1.0 {
		t.Errorf("items[0].qty should be untouched, got %v", items[0].(map[string]interface{})["qty"])
	}

	// invalid JSONPath → error
	if _, err := SetJSONPath(data, "$[", "x"); err == nil {
		t.Error("expected an error for an invalid JSONPath target")
	}
}

func TestEvaluateSelectorObject(t *testing.T) {
	state := &models.ExecutionState{
		StepsData: map[string]interface{}{
			"listProducts": map[string]interface{}{
				"outputs": map[string]interface{}{"body": sampleBody()},
			},
		},
	}
	respContext := map[string]interface{}{"body": sampleBody()}

	// JSON Pointer against $response.body
	sel := map[string]interface{}{"context": "$response.body", "selector": "/data/0/id", "type": "jsonpointer"}
	if v, err := EvaluateSelectorObject(sel, state, nil, respContext); err != nil || v != "p1" {
		t.Errorf("jsonpointer: got %v, %v; want p1", v, err)
	}

	// JSONPath filter against $response.body
	sel = map[string]interface{}{"context": "$response.body", "selector": "$.data[?(@.price < 10)].name", "type": "jsonpath"}
	if v, err := EvaluateSelectorObject(sel, state, nil, respContext); err != nil || v != "Hammer" {
		t.Errorf("jsonpath: got %v, %v; want Hammer", v, err)
	}

	// JSON Pointer against a $steps output (uses state, no response context)
	sel = map[string]interface{}{"context": "$steps.listProducts.outputs.body", "selector": "/data/1/id", "type": "jsonpointer"}
	if v, err := EvaluateSelectorObject(sel, state, nil, nil); err != nil || v != "p2" {
		t.Errorf("steps jsonpointer: got %v, %v; want p2", v, err)
	}

	// Explicit Expression Type Object
	sel = map[string]interface{}{"context": "$response.body", "selector": "$.data[1].name", "type": map[string]interface{}{"type": "jsonpath", "version": "rfc9535"}}
	if v, err := EvaluateSelectorObject(sel, state, nil, respContext); err != nil || v != "Drill" {
		t.Errorf("expr-type-object: got %v, %v; want Drill", v, err)
	}

	// XPath → clear "not supported" error
	sel = map[string]interface{}{"context": "$response.body", "selector": "/data/item", "type": "xpath"}
	if _, err := EvaluateSelectorObject(sel, state, nil, respContext); err == nil {
		t.Error("expected XPath to return a not-supported error")
	}

	// Unsupported version → error
	sel = map[string]interface{}{"context": "$response.body", "selector": "$.data", "type": map[string]interface{}{"type": "jsonpath", "version": "nope"}}
	if _, err := EvaluateSelectorObject(sel, state, nil, respContext); err == nil {
		t.Error("expected unsupported-version error")
	}
}
