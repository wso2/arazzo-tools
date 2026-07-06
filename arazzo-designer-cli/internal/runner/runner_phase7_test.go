package runner

import (
	"strings"
	"testing"

	"github.com/wso2/arazzo-designer-cli/internal/models"
	"github.com/wso2/arazzo-designer-cli/internal/telemetry"
)

func stepWithDeps(id string, deps ...string) map[string]interface{} {
	d := make([]interface{}, len(deps))
	for i, x := range deps {
		d[i] = x
	}
	return map[string]interface{}{"stepId": id, "dependsOn": d}
}

// The step-level dependsOn completion gate (spec §5.8.5.1): a prerequisite must have completed
// SUCCESSFULLY; no reordering, no triggering.
func TestCheckStepDependencies(t *testing.T) {
	r := &ArazzoRunner{}
	state := models.NewExecutionState("wf", nil, nil, nil)
	state.StepsStatus["okStep"] = models.StepStatusSuccess
	state.StepsStatus["failedStep"] = models.StepStatusFailure
	state.DependencyOutputs["ranWorkflow"] = map[string]interface{}{"x": 1}

	cases := []struct {
		name    string
		step    map[string]interface{}
		wantErr bool
	}{
		{"no deps", map[string]interface{}{"stepId": "B"}, false},
		{"satisfied local dep", stepWithDeps("B", "okStep"), false},
		{"unmet local dep (never ran)", stepWithDeps("B", "laterStep"), true},
		{"failed prerequisite does not satisfy", stepWithDeps("B", "failedStep"), true},
		{"multiple deps, one unmet", stepWithDeps("B", "okStep", "laterStep"), true},
		{"cross-workflow to completed workflow", stepWithDeps("B", "$workflows.ranWorkflow.steps.s"), false},
		{"cross-workflow to workflow that didn't run", stepWithDeps("B", "$workflows.nope.steps.s"), true},
		{"cross-document unsupported", stepWithDeps("B", "$sourceDescriptions.ext.wf.steps.s"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := r.checkStepDependencies(tc.step, state)
			if tc.wantErr && err == nil {
				t.Errorf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

// A circular WORKFLOW-level dependsOn must be reported clearly, not crash via infinite recursion.
// The cycle is detected inside executeDependencies before any step executes, so no HTTP is made.
func TestWorkflowDependsOnCycle(t *testing.T) {
	r := &ArazzoRunner{
		Sink: &telemetry.NoopSink{},
		Workflows: []interface{}{
			map[string]interface{}{
				"workflowId": "A",
				"dependsOn":  []interface{}{"B"},
				"steps":      []interface{}{map[string]interface{}{"stepId": "s1"}},
			},
			map[string]interface{}{
				"workflowId": "B",
				"dependsOn":  []interface{}{"A"},
				"steps":      []interface{}{map[string]interface{}{"stepId": "s1"}},
			},
		},
	}
	res := r.ExecuteWorkflow("A", nil)
	if res.Status != models.WorkflowStatusError {
		t.Fatalf("expected error status, got %v", res.Status)
	}
	if !strings.Contains(res.Error, "circular") {
		t.Errorf("expected a 'circular' dependency error, got %q", res.Error)
	}
}

// A self-referential workflow dependsOn is also caught.
func TestWorkflowDependsOnSelfCycle(t *testing.T) {
	r := &ArazzoRunner{
		Sink: &telemetry.NoopSink{},
		Workflows: []interface{}{
			map[string]interface{}{
				"workflowId": "solo",
				"dependsOn":  []interface{}{"solo"},
				"steps":      []interface{}{map[string]interface{}{"stepId": "s1"}},
			},
		},
	}
	res := r.ExecuteWorkflow("solo", nil)
	if res.Status != models.WorkflowStatusError || !strings.Contains(res.Error, "circular") {
		t.Errorf("expected a circular-dependency error, got status=%v err=%q", res.Status, res.Error)
	}
}
