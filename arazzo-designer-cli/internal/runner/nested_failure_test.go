package runner

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/failure"
	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

// kafkaBus is a source with no adapter, so any step against it fails with a known class and no
// network access.
const kafkaBus = `asyncapi: 3.0.0
info:
  title: Bus
  version: 1.0.0
servers:
  cluster:
    host: kafka.example.com:9092
    protocol: kafka
channels:
  events:
    address: events
`

// localBus runs in-process, so a step against it succeeds with no network access.
const localBus = `asyncapi: 3.0.0
info:
  title: Local
  version: 1.0.0
channels:
  events:
    address: local/events
`

func writeFlow(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"kafka.asyncapi.yaml": kafkaBus,
		"local.asyncapi.yaml": localBus,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "flow.arazzo.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A nested workflow that fails must fail the step that called it, and the parent with it. A nested
// step records no status of its own, so without explicit propagation the parent reported
// workflow_complete with no error at all - a failure one level down vanished from /run and MCP.
func TestNestedWorkflowFailureFailsTheParent(t *testing.T) {
	p := writeFlow(t, `arazzo: 1.1.0
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: kafkaBus
    url: ./kafka.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: parent
    steps:
      - stepId: callChild
        workflowId: child
  - workflowId: child
    steps:
      - stepId: boom
        channelPath: kafkaBus#/channels/events
        action: send
`)
	r, err := NewArazzoRunner(p, &models.RuntimeParams{}, &telemetry.NoopSink{})
	if err != nil {
		t.Fatal(err)
	}

	got := r.ExecuteWorkflow("parent", nil)
	if got.Status != models.WorkflowStatusError {
		t.Errorf("parent status = %v, want error - a failed nested workflow must not report complete", got.Status)
	}
	// The nested failure's OWN class, not a wrapper saying only "a nested workflow failed".
	if got.ErrorClass != string(failure.AdapterUnsupported) {
		t.Errorf("parent ErrorClass = %q, want %q", got.ErrorClass, failure.AdapterUnsupported)
	}
	if !strings.Contains(got.Error, "callChild") || !strings.Contains(got.Error, "not yet supported") {
		t.Errorf("parent Error should name the calling step and the underlying reason, got: %q", got.Error)
	}
}

// With more than one failed step, the reported message and class must describe the SAME step -
// the first to fail. They used to be chosen independently: the class by execution order, the
// message by scanning state.StepsStatus, which is a map and therefore iterated in random order.
func TestTheReportedErrorAndClassDescribeTheSameStep(t *testing.T) {
	p := writeFlow(t, `arazzo: 1.1.0
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: kafkaBus
    url: ./kafka.asyncapi.yaml
    type: asyncapi
  - name: localBus
    url: ./local.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: twoFailures
    steps:
      # Fails first, with adapter_unsupported, and hands on rather than ending the workflow.
      - stepId: firstToFail
        channelPath: kafkaBus#/channels/events
        action: send
        onFailure:
          - name: carryOn
            type: goto
            stepId: secondToFail
      # Fails second, with a DIFFERENT class, then hands on so the run reaches the end.
      - stepId: secondToFail
        channelPath: localBus#/channels/nosuchchannel
        action: send
        onFailure:
          - name: carryOn
            type: goto
            stepId: lastOne
      - stepId: lastOne
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            done: true
`)
	r, err := NewArazzoRunner(p, &models.RuntimeParams{}, &telemetry.NoopSink{})
	if err != nil {
		t.Fatal(err)
	}

	// Run it repeatedly: the old map scan picked an arbitrary failed step, so a single pass could
	// pass by luck. Map iteration is randomised per run, so ten passes would not.
	for i := 0; i < 10; i++ {
		got := r.ExecuteWorkflow("twoFailures", nil)
		if got.Status != models.WorkflowStatusError {
			t.Fatalf("run %d: status = %v, want error", i, got.Status)
		}
		if !strings.Contains(got.Error, "firstToFail") {
			t.Fatalf("run %d: Error should name the FIRST step to fail, got: %q", i, got.Error)
		}
		if got.ErrorClass != string(failure.AdapterUnsupported) {
			t.Fatalf("run %d: ErrorClass = %q, want %q - the class must belong to the same step the message names",
				i, got.ErrorClass, failure.AdapterUnsupported)
		}
	}
}

// spanSink records the trace events the graph would receive.
type spanSink struct {
	mu sync.Mutex
	ev []telemetry.TraceEvent
}

func (s *spanSink) Send(e telemetry.TraceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ev = append(s.ev, e)
}
func (s *spanSink) Shutdown() {}

// stepEnd returns the END span for one step, which is what the webview reads to colour a node and
// to show its failure reason.
func (s *spanSink) stepEnd(t *testing.T, stepID string) telemetry.TraceEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.ev {
		if e.ArazzoKind == telemetry.SpanKindStep && e.Lifecycle == telemetry.LifecycleEnd && e.Name == stepID {
			return e
		}
	}
	t.Fatalf("no end span for step %q", stepID)
	return telemetry.TraceEvent{}
}

// workflowSpan returns one lifecycle half of a workflow's span.
func (s *spanSink) workflowSpan(t *testing.T, workflowID string, lifecycle telemetry.Lifecycle) telemetry.TraceEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.ev {
		if e.ArazzoKind == telemetry.SpanKindWorkflow && e.Lifecycle == lifecycle && e.Name == workflowID {
			return e
		}
	}
	t.Fatalf("no %v span for workflow %q", lifecycle, workflowID)
	return telemetry.TraceEvent{}
}

// stepStart returns the START span for one step.
func (s *spanSink) stepStart(t *testing.T, stepID string) telemetry.TraceEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.ev {
		if e.ArazzoKind == telemetry.SpanKindStep && e.Lifecycle == telemetry.LifecycleStart && e.Name == stepID {
			return e
		}
	}
	t.Fatalf("no start span for step %q", stepID)
	return telemetry.TraceEvent{}
}

// A workflow that calls another is ONE run and must read as one trace: the nested run sits inside the
// step that called it. Two unrelated traces make a trace viewer show the child as a separate story
// with no link back to the step that caused it.
func TestNestedWorkflowSpansJoinTheCallersTrace(t *testing.T) {
	p := writeFlow(t, `arazzo: 1.1.0
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: localBus
    url: ./local.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: parent
    steps:
      - stepId: callChild
        workflowId: child
  - workflowId: child
    steps:
      - stepId: work
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            marker: ok
`)
	sink := &spanSink{}
	r, err := NewArazzoRunner(p, &models.RuntimeParams{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	r.ExecuteWorkflow("parent", nil)

	parentWorkflow := sink.workflowSpan(t, "parent", telemetry.LifecycleStart)
	childWorkflow := sink.workflowSpan(t, "child", telemetry.LifecycleStart)
	callingStep := sink.stepStart(t, "callChild")

	if parentWorkflow.ParentID != "" {
		t.Errorf("the top-level run should have no parent span, got %q", parentWorkflow.ParentID)
	}
	if childWorkflow.Context.TraceID != parentWorkflow.Context.TraceID {
		t.Errorf("the nested run should share the caller's trace: child %q, parent %q",
			childWorkflow.Context.TraceID, parentWorkflow.Context.TraceID)
	}
	if childWorkflow.ParentID != callingStep.Context.SpanID {
		t.Errorf("the nested run should sit under the step that called it: parent_id %q, step span %q",
			childWorkflow.ParentID, callingStep.Context.SpanID)
	}
	// The child's END must close the same span, so a viewer sees one span rather than two.
	if end := sink.workflowSpan(t, "child", telemetry.LifecycleEnd); end.ParentID != callingStep.Context.SpanID ||
		end.Context.SpanID != childWorkflow.Context.SpanID {
		t.Errorf("the child's end span must match its start: span %q/%q, parent %q",
			end.Context.SpanID, childWorkflow.Context.SpanID, end.ParentID)
	}
	// Everything emitted belongs to the one trace.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, e := range sink.ev {
		if e.Context.TraceID != parentWorkflow.Context.TraceID {
			t.Fatalf("%s %s is in trace %q, want the caller's %q",
				e.ArazzoKind, e.Name, e.Context.TraceID, parentWorkflow.Context.TraceID)
		}
	}
}

// One run is one trace, whichever way another workflow is reached: a dependency runs because this
// workflow needed it, and a goto hands over to it. Both used to start a trace of their own, leaving a
// viewer with two unrelated stories for a single run.
func TestDependencyAndGotoWorkflowsShareTheCallersTrace(t *testing.T) {
	p := writeFlow(t, `arazzo: 1.1.0
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: localBus
    url: ./local.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: main
    dependsOn:
      - prep
    steps:
      - stepId: handOver
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            marker: main
        onSuccess:
          - name: jump
            type: goto
            workflowId: continued
  - workflowId: prep
    steps:
      - stepId: prepare
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            marker: prep
  - workflowId: continued
    steps:
      - stepId: carryOn
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            marker: continued
`)
	sink := &spanSink{}
	r, err := NewArazzoRunner(p, &models.RuntimeParams{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.ExecuteWorkflow("main", nil); got.Status != models.WorkflowStatusWorkflowComplete {
		t.Fatalf("status = %v (%s)", got.Status, got.Error)
	}

	caller := sink.workflowSpan(t, "main", telemetry.LifecycleStart)

	// A dependency runs INSIDE the caller, whose span is open and waiting: a child of it.
	dep := sink.workflowSpan(t, "prep", telemetry.LifecycleStart)
	if dep.Context.TraceID != caller.Context.TraceID {
		t.Errorf("dependency trace = %q, want the caller's %q", dep.Context.TraceID, caller.Context.TraceID)
	}
	if dep.ParentID != caller.Context.SpanID {
		t.Errorf("dependency parent = %q, want the calling workflow's span %q", dep.ParentID, caller.Context.SpanID)
	}

	// A goto HANDS OVER: the caller has already ended, so the target is a second root in the same
	// trace. Parenting it would put a child outside its parent's lifetime.
	target := sink.workflowSpan(t, "continued", telemetry.LifecycleStart)
	if target.Context.TraceID != caller.Context.TraceID {
		t.Errorf("goto target trace = %q, want the caller's %q", target.Context.TraceID, caller.Context.TraceID)
	}
	if target.ParentID != "" {
		t.Errorf("goto target should be its own root, got parent %q", target.ParentID)
	}

	// One run, one trace - including the steps of all three workflows.
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, e := range sink.ev {
		if e.Context.TraceID != caller.Context.TraceID {
			t.Fatalf("%s %s is in trace %q, want %q", e.ArazzoKind, e.Name, e.Context.TraceID, caller.Context.TraceID)
		}
	}
}

// A run started on its own keeps its own trace - the change above must not make separate runs share one.
func TestSeparateRunsKeepSeparateTraces(t *testing.T) {
	p := writeFlow(t, `arazzo: 1.1.0
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: localBus
    url: ./local.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: solo
    steps:
      - stepId: work
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            marker: ok
`)
	sink := &spanSink{}
	r, err := NewArazzoRunner(p, &models.RuntimeParams{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	r.ExecuteWorkflow("solo", nil)
	first := sink.workflowSpan(t, "solo", telemetry.LifecycleStart)

	second := &spanSink{}
	r2, err := NewArazzoRunner(p, &models.RuntimeParams{}, second)
	if err != nil {
		t.Fatal(err)
	}
	r2.ExecuteWorkflow("solo", nil)
	other := second.workflowSpan(t, "solo", telemetry.LifecycleStart)

	if first.ParentID != "" || other.ParentID != "" {
		t.Errorf("a top-level run has no parent span, got %q and %q", first.ParentID, other.ParentID)
	}
	if first.Context.TraceID == other.Context.TraceID {
		t.Error("two separate runs must not share a trace id")
	}
}

// A step that calls another workflow must report that workflow's outcome, because the step itself
// does nothing else. Its span used to be closed inside ExecuteStep - BEFORE the nested run - so it
// always said error with no message: a failing child gave no reason in the graph, and a succeeding
// one still showed the node red.
func TestNestedWorkflowStepSpanReportsTheNestedOutcome(t *testing.T) {
	p := writeFlow(t, `arazzo: 1.1.0
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: kafkaBus
    url: ./kafka.asyncapi.yaml
    type: asyncapi
  - name: localBus
    url: ./local.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: parentFails
    steps:
      - stepId: callFailingChild
        workflowId: failingChild
  - workflowId: failingChild
    steps:
      - stepId: boom
        channelPath: kafkaBus#/channels/events
        action: send
  - workflowId: parentSucceeds
    steps:
      - stepId: callWorkingChild
        workflowId: workingChild
  - workflowId: workingChild
    steps:
      - stepId: work
        channelPath: localBus#/channels/events
        action: send
        requestBody:
          payload:
            marker: ok
`)

	t.Run("a failing child gives the calling step a reason", func(t *testing.T) {
		sink := &spanSink{}
		r, err := NewArazzoRunner(p, &models.RuntimeParams{}, sink)
		if err != nil {
			t.Fatal(err)
		}
		r.ExecuteWorkflow("parentFails", nil)

		span := sink.stepEnd(t, "callFailingChild")
		if span.StatusCode != telemetry.SpanStatusError {
			t.Errorf("status = %v, want error", span.StatusCode)
		}
		if !strings.Contains(span.StatusMessage, "not yet supported") {
			t.Errorf("the graph must show WHY the nested workflow failed, got: %q", span.StatusMessage)
		}
	})

	t.Run("a working child does not leave the step red", func(t *testing.T) {
		sink := &spanSink{}
		r, err := NewArazzoRunner(p, &models.RuntimeParams{}, sink)
		if err != nil {
			t.Fatal(err)
		}
		r.ExecuteWorkflow("parentSucceeds", nil)

		span := sink.stepEnd(t, "callWorkingChild")
		if span.StatusCode != telemetry.SpanStatusOK {
			t.Errorf("status = %v, want OK - a successful nested call must not render as a failed step", span.StatusCode)
		}
		if span.StatusMessage != "" {
			t.Errorf("a successful step carries no failure message, got: %q", span.StatusMessage)
		}
	})
}
