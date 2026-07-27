package evaluator

import (
	"strings"
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/models"
)

// ---- Phase 5: compound boolean criteria ----

func TestEvaluateSimpleCondition_Compound(t *testing.T) {
	state := models.NewExecutionState("wf", nil, nil, nil)
	ctx := map[string]interface{}{
		"statusCode": 200,
		"body": map[string]interface{}{
			"ok":    true,
			"count": 3,
			"msg":   "a && b", // operators inside a string must NOT be treated as syntax
		},
	}

	cases := []struct {
		cond string
		want bool
	}{
		// single comparison (regression)
		{`$statusCode == 200`, true},
		{`$statusCode == 201`, false},
		{`$statusCode != 201`, true},
		// && / ||
		{`$statusCode == 200 && $response.body.ok == true`, true},
		{`$statusCode == 201 && $response.body.ok == true`, false},
		{`$statusCode == 201 || $response.body.ok == true`, true},
		{`$statusCode == 201 || $response.body.ok == false`, false},
		// unary ! and parentheses
		{`!($statusCode == 201)`, true},
		{`!($statusCode == 200)`, false},
		{`($statusCode == 200 || $statusCode == 201) && $response.body.ok == true`, true},
		// numeric relational
		{`$response.body.count >= 3 && $response.body.count < 10`, true},
		{`$response.body.count > 3`, false},
		// truthy bare expression
		{`$response.body.ok`, true},
		// operators inside quotes are literal
		{`$response.body.msg == "a && b"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.cond, func(t *testing.T) {
			got := EvaluateSimpleCondition(tc.cond, state, nil, ctx)
			if got != tc.want {
				t.Errorf("EvaluateSimpleCondition(%q) = %v, want %v", tc.cond, got, tc.want)
			}
		})
	}
}

// Malformed conditions must fail safe to false (not silently evaluate a partial parse).
func TestEvaluateSimpleCondition_Malformed(t *testing.T) {
	state := models.NewExecutionState("wf", nil, nil, nil)
	ctx := map[string]interface{}{"statusCode": 200}
	cases := []string{
		"($statusCode == 200",         // missing closing paren
		"$statusCode == 200)",         // stray closing paren / unbalanced
		"$statusCode == 200 garbage",  // trailing unparsed input
		"($statusCode == 200 && )",    // empty operand inside group
	}
	for _, c := range cases {
		if EvaluateSimpleCondition(c, state, nil, ctx) {
			t.Errorf("malformed condition %q should be false", c)
		}
	}
	// sanity: the well-formed version is still true
	if !EvaluateSimpleCondition("($statusCode == 200)", state, nil, ctx) {
		t.Errorf("well-formed grouped condition should be true")
	}
}

// Escaped quotes inside a string literal must not end the string early (both quote-scanners).
func TestConditionParser_EscapedQuotes(t *testing.T) {
	// topLevelIndex: the only TOP-LEVEL "==" is the one AFTER the closing quote (index > 9),
	// not the one inside the literal (index 5). Without escape handling it would return 5.
	if got := topLevelIndex(`"a\" == b" == c`, "=="); got <= 9 {
		t.Errorf("topLevelIndex matched an operator inside the escaped-quote literal (index %d)", got)
	}
	// sanity: plain and fully-quoted cases still behave
	if got := topLevelIndex("a == b", "=="); got != 2 {
		t.Errorf("topLevelIndex plain: got %d, want 2", got)
	}
	if got := topLevelIndex(`"a == b"`, "=="); got != -1 {
		t.Errorf("topLevelIndex fully-quoted: got %d, want -1", got)
	}

	// readComparisonChunk: the "&&" inside an escaped-quote literal must not stop the chunk, so the
	// chunk includes the "b" that comes after it. Without escape handling the chunk would be `"a\"`.
	p := &condParser{s: `"a\" && b" tail`}
	if chunk := p.readComparisonChunk(); !strings.Contains(chunk, "b") {
		t.Errorf("readComparisonChunk stopped at && inside an escaped-quote literal: %q", chunk)
	}
}

// ---- Phase 5: new runtime-expression roots ----

func phase5State() *models.ExecutionState {
	st := models.NewExecutionState("wf", nil, nil, nil)
	st.Self = "https://example.com/main.arazzo.yaml"
	st.Components = map[string]interface{}{
		"parameters": map[string]interface{}{
			"foo": map[string]interface{}{"name": "foo", "value": "bar"},
		},
		"successActions": map[string]interface{}{
			"notify": map[string]interface{}{"name": "notify", "type": "end"},
		},
	}
	st.SourceDescriptionObjects = map[string]interface{}{
		"petstore": map[string]interface{}{
			"name": "petstore", "url": "https://api.example.com/openapi.yaml", "type": "openapi",
		},
		"flows": map[string]interface{}{
			"name": "flows", "url": "./other.arazzo.yaml", "type": "arazzo",
		},
	}
	st.WorkflowsByID = map[string]interface{}{
		"login": map[string]interface{}{"workflowId": "login", "summary": "Log in"},
	}
	return st
}

func phase5Sources() map[string]interface{} {
	return map[string]interface{}{
		"petstore": map[string]interface{}{
			"openapi": "3.0.0",
			"paths": map[string]interface{}{
				"/pets/{id}": map[string]interface{}{
					"get": map[string]interface{}{"operationId": "getPetById", "summary": "Get a pet"},
				},
			},
		},
		"flows": map[string]interface{}{
			"arazzo": "1.1.0",
			"workflows": []interface{}{
				map[string]interface{}{"workflowId": "checkout", "summary": "Checkout flow"},
			},
		},
	}
}

func TestEvaluate_Self(t *testing.T) {
	st := phase5State()
	if v := EvaluateExpression("$self", st, nil, nil); v != "https://example.com/main.arazzo.yaml" {
		t.Errorf("$self = %v, want the document URI", v)
	}
	// absent -> nil
	if v := EvaluateExpression("$self", models.NewExecutionState("wf", nil, nil, nil), nil, nil); v != nil {
		t.Errorf("$self with no Self set = %v, want nil", v)
	}
}

func TestEvaluate_Components(t *testing.T) {
	st := phase5State()
	if v := EvaluateExpression("$components.parameters.foo.value", st, nil, nil); v != "bar" {
		t.Errorf("$components.parameters.foo.value = %v, want bar", v)
	}
	if v := EvaluateExpression("$components.successActions.notify.type", st, nil, nil); v != "end" {
		t.Errorf("$components.successActions.notify.type = %v, want end", v)
	}
	if v := EvaluateExpression("$components.parameters.missing", st, nil, nil); v != nil {
		t.Errorf("unknown component = %v, want nil", v)
	}
}

func TestEvaluate_SourceDescriptions(t *testing.T) {
	st := phase5State()
	src := phase5Sources()

	// Priority 1: operationId match (OpenAPI) -> the operation object.
	op := EvaluateExpression("$sourceDescriptions.petstore.getPetById", st, src, nil)
	opMap, ok := op.(map[string]interface{})
	if !ok || opMap["operationId"] != "getPetById" {
		t.Errorf("$sourceDescriptions.petstore.getPetById = %v, want the getPetById operation", op)
	}
	// navigation onto the matched operation
	if v := EvaluateExpression("$sourceDescriptions.petstore.getPetById.summary", st, src, nil); v != "Get a pet" {
		t.Errorf("operation .summary = %v, want 'Get a pet'", v)
	}

	// Priority 1: workflowId match (Arazzo source) -> the workflow object.
	wf := EvaluateExpression("$sourceDescriptions.flows.checkout", st, src, nil)
	wfMap, ok := wf.(map[string]interface{})
	if !ok || wfMap["workflowId"] != "checkout" {
		t.Errorf("$sourceDescriptions.flows.checkout = %v, want the checkout workflow", wf)
	}

	// Priority 2: no id match -> Source Description Object field.
	if v := EvaluateExpression("$sourceDescriptions.petstore.url", st, src, nil); v != "https://api.example.com/openapi.yaml" {
		t.Errorf("$sourceDescriptions.petstore.url = %v, want the source url", v)
	}
	if v := EvaluateExpression("$sourceDescriptions.petstore.type", st, src, nil); v != "openapi" {
		t.Errorf("$sourceDescriptions.petstore.type = %v, want openapi", v)
	}
}

func TestEvaluate_SourceDescriptions_AsyncAPI(t *testing.T) {
	st := models.NewExecutionState("wf", nil, nil, nil)
	st.SourceDescriptionObjects = map[string]interface{}{
		"events": map[string]interface{}{"name": "events", "url": "./events.asyncapi.yaml", "type": "asyncapi"},
	}
	src := map[string]interface{}{
		"events": map[string]interface{}{
			"asyncapi": "3.0.0",
			"operations": map[string]interface{}{ // AsyncAPI 3.x: operations keyed by id
				"onOrderPlaced": map[string]interface{}{"action": "receive", "channel": "orders"},
			},
		},
	}
	// operationId match in an AsyncAPI source -> the operation object
	op := EvaluateExpression("$sourceDescriptions.events.onOrderPlaced", st, src, nil)
	if m, ok := op.(map[string]interface{}); !ok || m["action"] != "receive" {
		t.Errorf("asyncapi op = %v, want the onOrderPlaced operation", op)
	}
	// navigation onto the matched operation
	if v := EvaluateExpression("$sourceDescriptions.events.onOrderPlaced.action", st, src, nil); v != "receive" {
		t.Errorf("asyncapi op.action = %v, want receive", v)
	}
	// field fallback still works for AsyncAPI sources
	if v := EvaluateExpression("$sourceDescriptions.events.type", st, src, nil); v != "asyncapi" {
		t.Errorf("asyncapi source type = %v, want asyncapi", v)
	}
}

func TestEvaluate_Workflows(t *testing.T) {
	st := phase5State()
	if v := EvaluateExpression("$workflows.login.summary", st, nil, nil); v != "Log in" {
		t.Errorf("$workflows.login.summary = %v, want 'Log in'", v)
	}
	if v := EvaluateExpression("$workflows.nope.summary", st, nil, nil); v != nil {
		t.Errorf("unknown workflow = %v, want nil", v)
	}
}

func TestEmbeddedSerialization(t *testing.T) {
	st := models.NewExecutionState("wf", nil, nil, nil)
	st.Inputs = map[string]interface{}{
		"token": "abc",
		"obj":   map[string]interface{}{"a": float64(1)},
	}
	// primitive embeds as its text form
	if got := resolveTemplateString("Bearer {$inputs.token}", st, nil); got != "Bearer abc" {
		t.Errorf("primitive embed = %q, want 'Bearer abc'", got)
	}
	// object embeds as JSON (not Go's map[...] formatting)
	if got := resolveTemplateString("X={$inputs.obj}", st, nil); got != `X={"a":1}` {
		t.Errorf("object embed = %q, want X={\"a\":1}", got)
	}
	// unresolved expression is left in place
	if got := resolveTemplateString("V={$inputs.missing}", st, nil); got != "V={$inputs.missing}" {
		t.Errorf("unresolved embed = %q, want the placeholder preserved", got)
	}
}

func TestEvaluate_Message(t *testing.T) {
	st := models.NewExecutionState("wf", nil, nil, nil)
	ctx := map[string]interface{}{
		"message": map[string]interface{}{
			"header":  map[string]interface{}{"correlationId": "abc-123"},
			"payload": map[string]interface{}{"status": "confirmed", "items": []interface{}{map[string]interface{}{"id": 7}}},
		},
	}
	if v := EvaluateExpression("$message.payload.status", st, nil, ctx); v != "confirmed" {
		t.Errorf("$message.payload.status = %v, want confirmed", v)
	}
	if v := EvaluateExpression("$message.header.correlationId", st, nil, ctx); v != "abc-123" {
		t.Errorf("$message.header.correlationId = %v, want abc-123", v)
	}
	// JSON Pointer form routes through evaluateJSONPointer.
	if v := EvaluateExpression("$message.payload#/items/0/id", st, nil, ctx); v != 7 {
		t.Errorf("$message.payload#/items/0/id = %v, want 7", v)
	}
	// absent message -> nil (no async runtime in scope)
	if v := EvaluateExpression("$message.payload", st, nil, nil); v != nil {
		t.Errorf("$message with no message in context = %v, want nil", v)
	}
}
