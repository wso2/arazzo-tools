package server

import (
	"os"
	"slices"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arazzo/lsp/parser"
	"github.com/arazzo/lsp/validator"
)

// An AsyncAPI document written the way a real one is: the channel's message is a $ref into
// components.messages, and the contentType is declared over there.
const reffedAuditSpec = `asyncapi: 3.0.0
info:
  title: Audits
  version: 1.0.0
channels:
  audits:
    address: notify/audits
    messages:
      audit:
        $ref: '#/components/messages/audit'
  bare:
    address: notify/bare
operations:
  recordAudit:
    action: send
    channel:
      $ref: '#/channels/audits'
  sendBare:
    action: send
    channel:
      $ref: '#/channels/bare'
components:
  messages:
    audit:
      contentType: text/plain
      payload:
        type: string
`

// Both fixes, end to end through the server's resolver: a contentType behind a $ref is found, and it
// is found through every targeting form — including operationPath, which the runtime now executes too.
func TestServerResolvesContentTypeThroughRefAndEveryTargetingForm(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "audits.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(reffedAuditSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	arazzo := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: notify
    url: ./audits.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: viaChannel
        channelPath: notify#/channels/audits
        action: send
      - stepId: viaOperationId
        operationId: recordAudit
      - stepId: viaOperationPath
        operationPath: notify#/operations/recordAudit
      - stepId: viaBareChannel
        channelPath: notify#/channels/bare
        action: send
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "wf.arazzo.yaml"), arazzo)

	doc, err := parser.NewParser().Parse(arazzo)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	steps := doc.Workflows[0].Steps

	want := map[string]struct {
		contentTypes []string
		resolved     bool
	}{
		"viaChannel":       {[]string{"text/plain"}, true}, // $ref followed
		"viaOperationId":   {[]string{"text/plain"}, true}, // operation -> channel -> $ref
		"viaOperationPath": {[]string{"text/plain"}, true}, // the form the runtime previously could not execute
		"viaBareChannel":   {nil, true},                    // channel resolved, declares nothing
	}
	for i := range steps {
		step := steps[i]
		w, ok := want[step.StepID]
		if !ok {
			continue
		}
		ct, resolved := s.resolveStepMessageContentType(uri, arazzo, &step)
		if resolved != w.resolved || !slices.Equal(ct, w.contentTypes) {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", step.StepID, ct, resolved, w.contentTypes, w.resolved)
		}
	}
}

// The diagnostics that ride on top of that resolution, driven by the real server resolvers.
func TestServerContentTypeDiagnostics(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "audits.asyncapi.yaml")
	if err := os.WriteFile(specPath, []byte(reffedAuditSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	arazzo := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: notify
    url: ./audits.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: agrees
        operationPath: notify#/operations/recordAudit
        requestBody:
          contentType: text/plain
          payload: "x"
      - stepId: disagrees
        operationPath: notify#/operations/recordAudit
        requestBody:
          contentType: application/json
          payload: "x"
      - stepId: silent
        operationPath: notify#/operations/sendBare
        requestBody:
          payload: "x"
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "wf.arazzo.yaml"), arazzo)

	doc, err := parser.NewParser().Parse(arazzo)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := validator.NewValidator().
		WithStepActionResolver(func(step *parser.Step) (string, bool) {
			return s.resolveStepAsyncAction(uri, arazzo, step)
		}).
		WithStepContentTypeResolver(func(step *parser.Step) ([]string, bool) {
			return s.resolveStepMessageContentType(uri, arazzo, step)
		})
	errs := v.Validate(doc)

	var dump strings.Builder
	for _, e := range errs {
		dump.WriteString("\n  [" + e.Severity + "] " + e.Message)
	}

	find := func(severity, substr string) bool {
		for _, e := range errs {
			if e.Severity == severity && strings.Contains(e.Message, substr) {
				return true
			}
		}
		return false
	}

	// The $ref'd declaration agrees with the step -> nothing to report.
	if find("warning", "'agrees'") || find("information", "'agrees'") {
		t.Errorf("a step agreeing with the $ref'd declaration should be clean, got:%s", dump.String())
	}
	// The step disagrees with the $ref'd declaration -> warning. This is only reachable because both
	// the operationPath resolution and the $ref follow work.
	if !find("warning", "'disagrees'") {
		t.Errorf("a step disagreeing with the $ref'd declaration should warn, got:%s", dump.String())
	}
	// Channel declares nothing and neither does the step -> the JSON-fallback notice.
	if !find("information", "'silent'") {
		t.Errorf("a step with nothing declared anywhere should be reported as information, got:%s", dump.String())
	}
}
