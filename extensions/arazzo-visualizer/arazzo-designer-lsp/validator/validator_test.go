package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arazzo/lsp/parser"
)

// diagnose parses content and runs the full validation pipeline (schema validation +
// unknown-field detection), returning all diagnostics.
func diagnose(t *testing.T, content string) []ValidationError {
	t.Helper()
	doc, err := parser.NewParser().Parse(content)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	v := NewValidator()
	errs := v.Validate(doc)
	errs = append(errs, v.ValidateUnknownFields(content)...)
	return errs
}

func has(errs []ValidationError, severity, substr string) bool {
	for _, e := range errs {
		if e.Severity == severity && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

func countErrors(errs []ValidationError) int {
	n := 0
	for _, e := range errs {
		if e.Severity == "error" {
			n++
		}
	}
	return n
}

func dump(errs []ValidationError) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString("\n  [" + e.Severity + "] " + e.Message)
	}
	return b.String()
}

// docWith wraps a steps body (lines indented to 6 spaces under "steps:") into a complete,
// otherwise-valid v1.1.0 document. If sources is empty an OpenAPI source named "api" is used.
func docWith(sources, steps string) string {
	if sources == "" {
		sources = "  - name: api\n    url: ./api.yaml\n    type: openapi\n"
	}
	return `arazzo: "1.1.0"
info:
  title: Test
  version: "1.0.0"
sourceDescriptions:
` + sources + `workflows:
  - workflowId: wf
    steps:
` + steps
}

// ---- Phase 1: version & document shape ----

func TestVersions(t *testing.T) {
	for _, v := range []string{"1.0.0", "1.0.1", "1.1.0"} {
		content := strings.Replace(docWith("", "      - stepId: s1\n        operationId: op\n"), `arazzo: "1.1.0"`, `arazzo: "`+v+`"`, 1)
		errs := diagnose(t, content)
		if countErrors(errs) != 0 {
			t.Errorf("version %s should be valid, got:%s", v, dump(errs))
		}
	}
}

func TestInvalidVersion(t *testing.T) {
	content := strings.Replace(docWith("", "      - stepId: s1\n        operationId: op\n"), `arazzo: "1.1.0"`, `arazzo: "2.0.0"`, 1)
	errs := diagnose(t, content)
	if !has(errs, "error", "Invalid arazzo version") {
		t.Errorf("expected invalid version error, got:%s", dump(errs))
	}
}

func TestMissingArazzo(t *testing.T) {
	content := `info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: api
    url: ./api.yaml
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: op
`
	errs := diagnose(t, content)
	if !has(errs, "error", "Missing required field 'arazzo'") {
		t.Errorf("expected missing arazzo error, got:%s", dump(errs))
	}
}

// ---- Phase 2: target selector mutual exclusivity ----

func TestTargetSelector(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string // "" means no error expected
	}{
		{"none", "      - stepId: s1\n", "Must have one of"},
		{"single", "      - stepId: s1\n        operationId: op\n", ""},
		{"two", "      - stepId: s1\n        operationId: op\n        channelPath: bus#/c\n", "Can only have one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"
			errs := diagnose(t, docWith(sources, tc.body))
			if tc.wantErr == "" {
				if countErrors(errs) != 0 {
					t.Errorf("expected no errors, got:%s", dump(errs))
				}
			} else if !has(errs, "error", tc.wantErr) {
				t.Errorf("expected %q, got:%s", tc.wantErr, dump(errs))
			}
		})
	}
}

// ---- Phase 2: AsyncAPI step fields ----

func TestActionEnumAndChannel(t *testing.T) {
	bus := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"

	// invalid action value
	errs := diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: bus#/c\n        action: publish\n"))
	if !has(errs, "error", "Invalid action") {
		t.Errorf("expected invalid action error, got:%s", dump(errs))
	}

	// action without channelPath -> warning
	errs = diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        action: send\n"))
	if !has(errs, "warning", "'action' is only applicable to AsyncAPI") {
		t.Errorf("expected action-without-channelPath warning, got:%s", dump(errs))
	}

	// channelPath bad format
	errs = diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: nohashhere\n"))
	if !has(errs, "error", "channelPath' must be in the format") {
		t.Errorf("expected channelPath format error, got:%s", dump(errs))
	}

	// channelPath referencing a non-asyncapi source
	errs = diagnose(t, docWith("", "      - stepId: s1\n        channelPath: api#/c\n"))
	if !has(errs, "error", "must be 'asyncapi'") {
		t.Errorf("expected channelPath non-asyncapi error, got:%s", dump(errs))
	}
}

// `type` is optional on a Source Description Object, so a channelPath into a source that omits it
// must not be reported as a type violation.
func TestChannelPathAllowsUntypedSource(t *testing.T) {
	untyped := "  - name: bus\n    url: ./bus.yaml\n"
	errs := diagnose(t, docWith(untyped, "      - stepId: s1\n        channelPath: bus#/channels/orders\n        action: send\n"))
	if has(errs, "error", "must be 'asyncapi'") {
		t.Errorf("a source without a declared type should not trigger the type error, got:%s", dump(errs))
	}
}

func TestChannelPathRequiresAction(t *testing.T) {
	bus := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"
	// channelPath present but no action -> error (direction undefined)
	errs := diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: bus#/channels/c\n"))
	if !has(errs, "error", "must also specify 'action'") {
		t.Errorf("expected channelPath-without-action error, got:%s", dump(errs))
	}
	// channelPath WITH action -> no such error
	errs = diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: bus#/channels/c\n        action: send\n"))
	if has(errs, "error", "must also specify 'action'") {
		t.Errorf("channelPath with action should not trigger the error, got:%s", dump(errs))
	}
}

// ---- step target references: both source-reference spellings, all three targeting fields ----

// The spec REQUIRES a runtime expression to identify the source document in channelPath /
// operationPath ("{$sourceDescriptions.<name>.url}#..."), while a bare source name is the common
// shorthand. Both must resolve to the same source and neither may be reported as unknown.
func TestSourceRefFormsAccepted(t *testing.T) {
	bus := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"
	api := "  - name: api\n    url: ./api.yaml\n    type: openapi\n"

	channelPaths := []string{
		"bus#/channels/orders",
		"'{$sourceDescriptions.bus.url}#/channels/orders'",
	}
	for _, cp := range channelPaths {
		errs := diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: "+cp+"\n        action: send\n"))
		if has(errs, "warning", "unknown source description") {
			t.Errorf("channelPath %s must resolve to the declared source, got:%s", cp, dump(errs))
		}
		if has(errs, "error", "must be in the format") {
			t.Errorf("channelPath %s should be a valid format, got:%s", cp, dump(errs))
		}
	}

	operationPaths := []string{
		"api#/paths/~1products/get",
		"'{$sourceDescriptions.api.url}#/paths/~1products/get'",
	}
	for _, op := range operationPaths {
		errs := diagnose(t, docWith(api, "      - stepId: s1\n        operationPath: "+op+"\n"))
		if has(errs, "warning", "unknown source description") {
			t.Errorf("operationPath %s must resolve to the declared source, got:%s", op, dump(errs))
		}
		if has(errs, "error", "must be in the format") {
			t.Errorf("operationPath %s should be a valid format, got:%s", op, dump(errs))
		}
	}

	// Scoped operationId naming a declared source is fine.
	errs := diagnose(t, docWith(api, "      - stepId: s1\n        operationId: $sourceDescriptions.api.getProducts\n"))
	if has(errs, "warning", "unknown source description") {
		t.Errorf("scoped operationId must resolve to the declared source, got:%s", dump(errs))
	}
}

// Each targeting field must report a source description that was never declared.
func TestUnknownSourceReported(t *testing.T) {
	bus := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"
	api := "  - name: api\n    url: ./api.yaml\n    type: openapi\n"

	errs := diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: ghost#/channels/orders\n        action: send\n"))
	if !has(errs, "warning", "'channelPath' references unknown source description 'ghost'") {
		t.Errorf("expected unknown-source warning for channelPath, got:%s", dump(errs))
	}

	errs = diagnose(t, docWith(api, "      - stepId: s1\n        operationPath: ghost#/paths/~1p/get\n"))
	if !has(errs, "warning", "'operationPath' references unknown source description 'ghost'") {
		t.Errorf("expected unknown-source warning for operationPath, got:%s", dump(errs))
	}

	errs = diagnose(t, docWith(api, "      - stepId: s1\n        operationId: $sourceDescriptions.ghost.getProducts\n"))
	if !has(errs, "warning", "'operationId' references unknown source description 'ghost'") {
		t.Errorf("expected unknown-source warning for scoped operationId, got:%s", dump(errs))
	}
}

func TestOperationPathValidation(t *testing.T) {
	api := "  - name: api\n    url: ./api.yaml\n    type: openapi\n"

	// missing '#' -> format error
	errs := diagnose(t, docWith(api, "      - stepId: s1\n        operationPath: nohashhere\n"))
	if !has(errs, "error", "'operationPath' must be in the format") {
		t.Errorf("expected operationPath format error, got:%s", dump(errs))
	}

	// an arazzo source describes workflows, not operations
	arazzo := "  - name: other\n    url: ./other.arazzo.yaml\n    type: arazzo\n"
	errs = diagnose(t, docWith(arazzo, "      - stepId: s1\n        operationPath: other#/workflows/wf\n"))
	if !has(errs, "error", "use 'workflowId' to target an Arazzo workflow") {
		t.Errorf("expected arazzo-source operationPath error, got:%s", dump(errs))
	}

	// operationPath into an AsyncAPI operation is allowed (spec says "an operation", not "a REST one")
	bus := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"
	errs = diagnose(t, docWith(bus, "      - stepId: s1\n        operationPath: bus#/operations/placeOrder\n"))
	if countErrors(errs) != 0 {
		t.Errorf("operationPath into an AsyncAPI operation should be valid, got:%s", dump(errs))
	}
}

func TestMalformedOperationIdExpression(t *testing.T) {
	api := "  - name: api\n    url: ./api.yaml\n    type: openapi\n"
	// a '$' expression that isn't the scoped form is malformed
	errs := diagnose(t, docWith(api, "      - stepId: s1\n        operationId: $sourceDescriptions.api\n"))
	if !has(errs, "error", "must be '$sourceDescriptions.<name>.<operationId>'") {
		t.Errorf("expected malformed operationId expression error, got:%s", dump(errs))
	}
	// a bare operationId is always fine (resolved across sources at runtime)
	errs = diagnose(t, docWith(api, "      - stepId: s1\n        operationId: getProducts\n"))
	if countErrors(errs) != 0 {
		t.Errorf("bare operationId should be valid, got:%s", dump(errs))
	}
}

func TestTimeout(t *testing.T) {
	errs := diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        timeout: -5\n"))
	if !has(errs, "error", "'timeout' must be a non-negative integer") {
		t.Errorf("expected timeout error, got:%s", dump(errs))
	}
}

func TestCorrelationId(t *testing.T) {
	bus := "  - name: bus\n    url: ./bus.yaml\n    type: asyncapi\n"

	// correlationId without channelPath -> warning
	errs := diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        correlationId: $message.payload.id\n"))
	if !has(errs, "warning", "only meaningful on AsyncAPI steps") {
		t.Errorf("expected correlationId-without-channelPath warning, got:%s", dump(errs))
	}

	// correlationId on a send step -> warning (only applicable to receive)
	errs = diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: bus#/c\n        action: send\n        correlationId: $message.payload.id\n"))
	if !has(errs, "warning", "action 'receive'") {
		t.Errorf("expected correlationId-requires-receive warning, got:%s", dump(errs))
	}

	// correlationId on a receive step -> ok
	errs = diagnose(t, docWith(bus, "      - stepId: s1\n        channelPath: bus#/c\n        action: receive\n        correlationId: $message.payload.id\n        successCriteria:\n          - condition: $message.payload.id == 1\n"))
	if has(errs, "warning", "correlationId") {
		t.Errorf("receive step with correlationId should not warn, got:%s", dump(errs))
	}
}

// ---- Phase 2: dependsOn ----

func TestDependsOn(t *testing.T) {
	twoSteps := func(dep string) string {
		return "      - stepId: first\n        operationId: op1\n      - stepId: second\n        operationId: op2\n        dependsOn:\n          - " + dep + "\n"
	}
	cases := []struct {
		name    string
		dep     string
		wantErr string
	}{
		{"bare-existing", "first", ""},
		{"bare-missing", "nope", "unknown step 'nope'"},
		{"self", "second", "must not reference the step itself"},
		{"cross-workflow-good", "$workflows.wf.steps.first", ""},
		{"cross-workflow-bad-wf", "$workflows.ghost.steps.first", "unknown workflow 'ghost'"},
		{"external-form", "$sourceDescriptions.ext.otherWf.steps.s9", ""},
		{"bad-expr", "$nonsense.foo", "invalid dependsOn reference"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := diagnose(t, docWith("", twoSteps(tc.dep)))
			if tc.wantErr == "" {
				if countErrors(errs) != 0 {
					t.Errorf("expected no errors, got:%s", dump(errs))
				}
			} else if !has(errs, "error", tc.wantErr) {
				t.Errorf("expected %q, got:%s", tc.wantErr, dump(errs))
			}
		})
	}
}

func TestDependsOnCycle(t *testing.T) {
	// a -> b -> a is a local cycle with no resolvable order.
	cyc := "      - stepId: a\n        operationId: op1\n        dependsOn:\n          - b\n" +
		"      - stepId: b\n        operationId: op2\n        dependsOn:\n          - a\n"
	if errs := diagnose(t, docWith("", cyc)); !has(errs, "error", "circular dependsOn") {
		t.Errorf("expected a circular dependsOn error, got:%s", dump(errs))
	}

	// a -> b (b defined later) is a forward, non-cyclic dependency — must NOT be flagged as a cycle.
	acyclic := "      - stepId: a\n        operationId: op1\n        dependsOn:\n          - b\n" +
		"      - stepId: b\n        operationId: op2\n"
	if errs := diagnose(t, docWith("", acyclic)); has(errs, "error", "circular dependsOn") {
		t.Errorf("acyclic forward dependency should not report a cycle, got:%s", dump(errs))
	}
}

func TestWorkflowDependsOnCycle(t *testing.T) {
	// threeWf builds a document with workflows A, B, C, each given the dependsOn line in depA/depB/depC
	// (an empty string omits dependsOn for that workflow).
	threeWf := func(depA, depB, depC string) string {
		wf := func(id, dep string) string {
			s := "  - workflowId: " + id + "\n"
			if dep != "" {
				s += "    dependsOn:\n      - " + dep + "\n"
			}
			return s + "    steps:\n      - stepId: s_" + id + "\n        operationId: op\n"
		}
		return `arazzo: "1.1.0"
info:
  title: Test
  version: "1.0.0"
sourceDescriptions:
  - name: api
    url: ./api.yaml
    type: openapi
workflows:
` + wf("A", depA) + wf("B", depB) + wf("C", depC)
	}

	// A -> B, B -> A: direct two-workflow cycle.
	if errs := diagnose(t, threeWf("B", "A", "")); !has(errs, "error", "circular dependsOn") {
		t.Errorf("expected a circular workflow dependsOn error (A<->B), got:%s", dump(errs))
	}

	// A -> B -> C -> A: three-workflow cycle.
	if errs := diagnose(t, threeWf("B", "C", "A")); !has(errs, "error", "circular dependsOn") {
		t.Errorf("expected a circular workflow dependsOn error (A->B->C->A), got:%s", dump(errs))
	}

	// A -> A: self-cycle.
	if errs := diagnose(t, threeWf("A", "", "")); !has(errs, "error", "circular dependsOn") {
		t.Errorf("expected a self-cycle workflow dependsOn error, got:%s", dump(errs))
	}

	// A -> B, B -> C, C -> (nothing): acyclic chain — must NOT be flagged.
	if errs := diagnose(t, threeWf("B", "C", "")); has(errs, "error", "circular dependsOn") {
		t.Errorf("acyclic workflow chain should not report a cycle, got:%s", dump(errs))
	}
}

// ---- Phase 2: successCriteria, parameters, actions ----

func TestEmptySuccessCriteria(t *testing.T) {
	errs := diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        successCriteria: []\n"))
	if !has(errs, "error", "'successCriteria' is defined but empty") {
		t.Errorf("expected empty successCriteria error, got:%s", dump(errs))
	}
}

func TestParameterInEnum(t *testing.T) {
	// querystring is valid (v1.1.0)
	errs := diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        parameters:\n          - name: q\n            in: querystring\n            value: x\n"))
	if countErrors(errs) != 0 {
		t.Errorf("querystring should be valid, got:%s", dump(errs))
	}
	// bogus in value
	errs = diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        parameters:\n          - name: q\n            in: body\n            value: x\n"))
	if !has(errs, "error", "invalid 'in' value 'body'") {
		t.Errorf("expected invalid in error, got:%s", dump(errs))
	}
}

func TestActionParameters(t *testing.T) {
	// action params with 'in' -> error
	body := "      - stepId: s1\n        operationId: op\n        onSuccess:\n          - name: go\n            type: goto\n            workflowId: wf\n            parameters:\n              - name: p\n                in: query\n                value: x\n"
	errs := diagnose(t, docWith("", body))
	if !has(errs, "error", "'in' field MUST NOT be used") {
		t.Errorf("expected action-param in error, got:%s", dump(errs))
	}

	// action params without workflowId -> error
	body = "      - stepId: s1\n        operationId: op\n        onSuccess:\n          - name: e\n            type: end\n            parameters:\n              - name: p\n                value: x\n"
	errs = diagnose(t, docWith("", body))
	if !has(errs, "error", "no 'workflowId'") {
		t.Errorf("expected params-without-workflowId error, got:%s", dump(errs))
	}
}

func TestActionTypeAndTarget(t *testing.T) {
	// invalid success action type
	body := "      - stepId: s1\n        operationId: op\n        onSuccess:\n          - name: x\n            type: retry\n"
	errs := diagnose(t, docWith("", body))
	if !has(errs, "error", "invalid type 'retry'") {
		t.Errorf("expected invalid success action type, got:%s", dump(errs))
	}

	// stepId + workflowId both set
	body = "      - stepId: s1\n        operationId: op\n        onSuccess:\n          - name: x\n            type: goto\n            stepId: s1\n            workflowId: wf\n"
	errs = diagnose(t, docWith("", body))
	if !has(errs, "error", "mutually exclusive") {
		t.Errorf("expected mutual-exclusivity error, got:%s", dump(errs))
	}
}

func TestComponentReference(t *testing.T) {
	base := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: api
    url: ./api.yaml
    type: openapi
components:
  successActions:
    Known:
      name: Known
      type: end
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: op
        onSuccess:
          - reference: $components.successActions.%s
`
	// resolves
	errs := diagnose(t, strings.ReplaceAll(base, "%s", "Known"))
	if has(errs, "error", "does not resolve") {
		t.Errorf("valid reference should resolve, got:%s", dump(errs))
	}
	// missing
	errs = diagnose(t, strings.ReplaceAll(base, "%s", "Ghost"))
	if !has(errs, "error", "does not resolve") {
		t.Errorf("expected unresolved reference error, got:%s", dump(errs))
	}

	// wrong section: a parameter reference must point to $components.parameters.<key>,
	// so referencing a successAction from a parameter position is rejected.
	wrongSection := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: api
    url: ./api.yaml
    type: openapi
components:
  successActions:
    Known:
      name: Known
      type: end
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: op
        parameters:
          - reference: $components.successActions.Known
`
	errs = diagnose(t, wrongSection)
	if !has(errs, "error", "must point to '$components.parameters") {
		t.Errorf("expected wrong-section error for a parameter referencing successActions, got:%s", dump(errs))
	}
}

// ---- Phase 2: $self & source type ----

func TestSelf(t *testing.T) {
	withSelf := func(s string) string {
		return strings.Replace(docWith("", "      - stepId: s1\n        operationId: op\n"),
			"info:", "$self: "+s+"\ninfo:", 1)
	}
	if errs := diagnose(t, withSelf("https://example.com/wf.yaml")); has(errs, "error", "$self") {
		t.Errorf("valid $self should pass, got:%s", dump(errs))
	}
	if errs := diagnose(t, withSelf("https://example.com/wf.yaml#/frag")); !has(errs, "error", "MUST NOT contain a fragment") {
		t.Errorf("expected $self fragment error, got:%s", dump(errs))
	}
}

func TestSourceType(t *testing.T) {
	errs := diagnose(t, docWith("  - name: api\n    url: ./api.yaml\n    type: grpc\n", "      - stepId: s1\n        operationId: op\n"))
	if !has(errs, "error", "Invalid type 'grpc'") {
		t.Errorf("expected invalid source type error, got:%s", dump(errs))
	}
}

// ---- Expression Type Object (Phase 4 validation) ----

func TestExpressionType(t *testing.T) {
	crit := func(typeBlock string) string {
		return "      - stepId: s1\n        operationId: op\n        successCriteria:\n" +
			"          - context: $response.body\n            condition: \"$.x\"\n" + typeBlock
	}
	cases := []struct {
		name    string
		typeYML string
		wantErr string // "" = expect no error
	}{
		{"valid object", "            type:\n              type: jsonpath\n              version: rfc9535\n", ""},
		{"valid string short form", "            type: jsonpath\n", ""},
		{"missing version", "            type:\n              type: jsonpath\n", "missing required 'version'"},
		{"bad version", "            type:\n              type: jsonpath\n              version: nope\n", "unsupported version"},
		{"bad dialect", "            type:\n              type: yaml\n              version: rfc9535\n", "invalid 'type'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := diagnose(t, docWith("", crit(tc.typeYML)))
			if tc.wantErr == "" {
				if has(errs, "error", "Expression Type Object") {
					t.Errorf("expected no Expression Type Object error, got:%s", dump(errs))
				}
			} else if !has(errs, "error", tc.wantErr) {
				t.Errorf("expected %q, got:%s", tc.wantErr, dump(errs))
			}
		})
	}
}

// ---- Unknown fields ----

func TestUnknownFields(t *testing.T) {
	// step typo
	errs := diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        chanelPath: bus#/c\n"))
	if !has(errs, "warning", "Unknown field 'chanelPath' in a step") {
		t.Errorf("expected unknown step field warning, got:%s", dump(errs))
	}
	// $ref instead of reference on a parameter
	errs = diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        parameters:\n          - $ref: $components.parameters.X\n"))
	if !has(errs, "warning", "Unknown field '$ref' in a parameter") {
		t.Errorf("expected unknown $ref warning, got:%s", dump(errs))
	}
	// x- extension allowed
	errs = diagnose(t, docWith("  - name: api\n    url: ./api.yaml\n    type: openapi\n    x-internal: true\n", "      - stepId: s1\n        operationId: op\n"))
	if has(errs, "warning", "Unknown field 'x-internal'") {
		t.Errorf("x- extension should be allowed, got:%s", dump(errs))
	}

	// 'value' is only valid on a parameter Reusable Object — on an action it must be flagged.
	errs = diagnose(t, docWith("", "      - stepId: s1\n        operationId: op\n        onSuccess:\n          - name: a\n            type: end\n            value: x\n"))
	if !has(errs, "warning", "Unknown field 'value' in a success action") {
		t.Errorf("expected unknown 'value' warning on an action, got:%s", dump(errs))
	}
}

// ---- Example fixtures ----

func TestExampleFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "..", "examples", "async_test")
	clean := []string{"v101-backward-compat.arazzo.yaml", "v110-openapi-new-fields.arazzo.yaml", "v110-asyncapi-channel.arazzo.yaml"}
	for _, name := range clean {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Skipf("fixture not found: %v", err)
			}
			errs := diagnose(t, string(content))
			if len(errs) != 0 {
				t.Errorf("expected zero diagnostics for %s, got:%s", name, dump(errs))
			}
		})
	}

	// The Phase 8 targeting examples double as fixtures: 04 must validate cleanly (every legal
	// targeting form, in both source-reference spellings), 05 must produce exactly the diagnostics
	// its header documents.
	t.Run("phase8/04-step-targeting-forms.arazzo.yaml", func(t *testing.T) {
		content, err := os.ReadFile(filepath.Join(dir, "phase8", "04-step-targeting-forms.arazzo.yaml"))
		if err != nil {
			t.Skipf("fixture not found: %v", err)
		}
		errs := diagnose(t, string(content))
		if len(errs) != 0 {
			t.Errorf("every targeting form should validate cleanly, got:%s", dump(errs))
		}
	})

	t.Run("phase8/05-targeting-validation.arazzo.yaml", func(t *testing.T) {
		content, err := os.ReadFile(filepath.Join(dir, "phase8", "05-targeting-validation.arazzo.yaml"))
		if err != nil {
			t.Skipf("fixture not found: %v", err)
		}
		errs := diagnose(t, string(content))

		expected := []struct{ severity, substr string }{
			{"warning", "'channelPath' references unknown source description 'ghostBus'"},
			{"warning", "'operationPath' references unknown source description 'ghostApi'"},
			{"warning", "'operationId' references unknown source description 'ghostApi'"},
			{"error", "'operationPath' must be in the format"},
			{"error", "must be '$sourceDescriptions.<name>.<operationId>'"},
			{"error", "use 'workflowId' to target an Arazzo workflow"},
		}
		for _, e := range expected {
			if !has(errs, e.severity, e.substr) {
				t.Errorf("expected %s containing %q, got:%s", e.severity, e.substr, dump(errs))
			}
		}

		// The `goodTargets` workflow uses both legal source-reference spellings; neither may be
		// reported as unknown. Exactly three unknown-source warnings are expected — the ghost ones.
		unknown := 0
		for _, e := range errs {
			if strings.Contains(e.Message, "unknown source description") {
				unknown++
				if !strings.Contains(e.Message, "ghost") {
					t.Errorf("a legal source reference was reported unknown: %s", e.Message)
				}
			}
		}
		if unknown != 3 {
			t.Errorf("expected 3 unknown-source warnings, got %d:%s", unknown, dump(errs))
		}
	})

	t.Run("invalid-v110.arazzo.yaml", func(t *testing.T) {
		content, err := os.ReadFile(filepath.Join(dir, "invalid-v110.arazzo.yaml"))
		if err != nil {
			t.Skipf("fixture not found: %v", err)
		}
		errs := diagnose(t, string(content))
		if !has(errs, "error", "info.title") {
			t.Errorf("expected missing info.title error, got:%s", dump(errs))
		}
	})
}
