package server

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/arazzo/lsp/parser"
	"github.com/arazzo/lsp/validator"
)

// An AsyncAPI document covering every shape a Correlation ID Object takes: declared inline, reached
// through a $ref'd message, reached through a $ref'd Correlation ID Object, and absent entirely.
const correlationSpec = `asyncapi: 3.0.0
info:
  title: Orders
  version: 1.0.0
channels:
  orders:
    address: orders/new
    messages:
      order:
        correlationId:
          location: $message.header#/correlationId
  audits:
    address: audit/log
    messages:
      audit:
        $ref: '#/components/messages/audit'
  shipments:
    address: ship/new
    messages:
      shipment:
        correlationId:
          $ref: '#/components/correlationIds/byTrackingId'
  bare:
    address: legacy/events
    messages:
      event:
        contentType: application/json
operations:
  onOrder:
    action: receive
    channel:
      $ref: '#/channels/orders'
  onBare:
    action: receive
    channel:
      $ref: '#/channels/bare'
components:
  messages:
    audit:
      correlationId:
        location: $message.payload#/auditId
  correlationIds:
    byTrackingId:
      location: $message.header#/trackingId
`

// The resolver itself, driven through the real server — including the $ref'd forms and every
// targeting form. ensureSourcesIndexed is what makes this deterministic; without it this races the
// background indexer and silently returns (nil, "", false).
func TestServerResolvesCorrelationLocation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orders.asyncapi.yaml"), []byte(correlationSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	arazzo := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: orderBus
    url: ./orders.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: viaChannel
        channelPath: orderBus#/channels/orders
        action: receive
      - stepId: viaRefdMessage
        channelPath: orderBus#/channels/audits
        action: receive
      - stepId: viaRefdCorrelationId
        channelPath: orderBus#/channels/shipments
        action: receive
      - stepId: viaOperationId
        operationId: onOrder
      - stepId: viaOperationPath
        operationPath: orderBus#/operations/onOrder
      - stepId: undeclared
        channelPath: orderBus#/channels/bare
        action: receive
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "wf.arazzo.yaml"), arazzo)

	doc, err := parser.NewParser().Parse(arazzo)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	want := map[string][]string{
		"viaChannel":           {"$message.header#/correlationId"},
		"viaRefdMessage":       {"$message.payload#/auditId"},   // message is a $ref
		"viaRefdCorrelationId": {"$message.header#/trackingId"}, // the Correlation ID Object is a $ref
		"viaOperationId":       {"$message.header#/correlationId"},
		"viaOperationPath":     {"$message.header#/correlationId"},
		"undeclared":           nil, // resolved, but the document declares nothing
	}

	steps := doc.Workflows[0].Steps
	for i := range steps {
		step := steps[i]
		w, ok := want[step.StepID]
		if !ok {
			continue
		}
		got, source, resolved := s.resolveStepCorrelationLocation(uri, arazzo, &step)
		if !resolved {
			t.Errorf("%s: expected the channel to resolve", step.StepID)
			continue
		}
		if !slices.Equal(got, w) {
			t.Errorf("%s: got %v, want %v", step.StepID, got, w)
		}
		// The advice has to name something the author can act on.
		if source == "" {
			t.Errorf("%s: expected a source name to point the author at", step.StepID)
		}
	}
}

// The diagnostic that rides on that resolution, driven by the real server resolvers.
func TestServerCorrelationLocationDiagnostic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orders.asyncapi.yaml"), []byte(correlationSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	arazzo := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: orderBus
    url: ./orders.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: located
        channelPath: orderBus#/channels/orders
        action: receive
        correlationId: $inputs.token
      - stepId: unlocated
        channelPath: orderBus#/channels/bare
        action: receive
        correlationId: $inputs.token
      - stepId: unfiltered
        channelPath: orderBus#/channels/bare
        action: receive
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "wf.arazzo.yaml"), arazzo)

	doc, err := parser.NewParser().Parse(arazzo)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := validator.NewValidator().
		WithStepActionResolver(func(step *parser.Step) (string, bool) {
			return s.resolveStepAsyncAction(uri, arazzo, step)
		}).
		WithStepCorrelationLocationResolver(func(step *parser.Step) ([]string, string, bool) {
			return s.resolveStepCorrelationLocation(uri, arazzo, step)
		}).
		Validate(doc)

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

	// The channel says where the id lives -> nothing to report.
	if find("information", "'located'") {
		t.Errorf("a channel declaring a location should be clean, got:%s", dump.String())
	}
	// It does not -> information, naming the source description the author must edit.
	if !find("information", "'unlocated'") {
		t.Errorf("an undeclared location should be reported, got:%s", dump.String())
	}
	if !find("information", "orderBus") {
		t.Errorf("the advice should name the source description, got:%s", dump.String())
	}
	// No correlationId at all -> unfiltered; there is nothing to locate, and that case has its own
	// warning. Reporting a location here would be advice about a filter the step does not use.
	if find("information", "'unfiltered'") {
		t.Errorf("a receive with no correlationId needs no location advice, got:%s", dump.String())
	}
}
