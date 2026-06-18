package parser

import "testing"

const v110Doc = `arazzo: "1.1.0"
$self: https://example.com/wf.yaml
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: bus
    url: ./bus.yaml
    type: asyncapi
components:
  successActions:
    Done:
      name: Done
      type: end
workflows:
  - workflowId: wf
    steps:
      - stepId: send
        channelPath: bus#/channels/orders
        action: send
        timeout: 5000
        parameters:
          - name: q
            in: querystring
            value: $inputs.q
        requestBody:
          contentType: application/json
          payload:
            a: 1
          replacements:
            - target: /a
              targetSelectorType: jsonpointer
              value: 2
      - stepId: recv
        channelPath: bus#/channels/orders
        action: receive
        correlationId: $message.payload.id
        dependsOn:
          - send
        successCriteria:
          - condition: $message.payload.id == 1
        onSuccess:
          - reference: $components.successActions.Done
`

func TestParseV110Fields(t *testing.T) {
	doc, err := NewParser().Parse(v110Doc)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if doc.Arazzo != "1.1.0" {
		t.Errorf("arazzo = %q, want 1.1.0", doc.Arazzo)
	}
	if doc.Self != "https://example.com/wf.yaml" {
		t.Errorf("$self = %q", doc.Self)
	}
	if len(doc.SourceDescriptions) != 1 || doc.SourceDescriptions[0].Type != "asyncapi" {
		t.Fatalf("source description not parsed: %+v", doc.SourceDescriptions)
	}
	if len(doc.Workflows) != 1 || len(doc.Workflows[0].Steps) != 2 {
		t.Fatalf("steps not parsed: %+v", doc.Workflows)
	}
	send := doc.Workflows[0].Steps[0]
	if send.ChannelPath == "" || send.Action != "send" || send.Timeout != 5000 {
		t.Errorf("send step v1.1.0 fields wrong: channelPath=%q action=%q timeout=%d", send.ChannelPath, send.Action, send.Timeout)
	}
	if len(send.Parameters) != 1 || send.Parameters[0].In != "querystring" {
		t.Errorf("querystring parameter not parsed: %+v", send.Parameters)
	}
	if send.RequestBody == nil || len(send.RequestBody.Replacements) != 1 || send.RequestBody.Replacements[0].Target != "/a" {
		t.Errorf("replacements not parsed: %+v", send.RequestBody)
	}
	recv := doc.Workflows[0].Steps[1]
	if recv.Action != "receive" || recv.CorrelationID == "" || len(recv.DependsOn) != 1 || recv.DependsOn[0] != "send" {
		t.Errorf("receive step v1.1.0 fields wrong: %+v", recv)
	}
	if len(recv.OnSuccess) != 1 || recv.OnSuccess[0].Reference != "$components.successActions.Done" {
		t.Errorf("reusable onSuccess reference not parsed: %+v", recv.OnSuccess)
	}
	if doc.Components == nil || len(doc.Components.SuccessActions) != 1 {
		t.Errorf("components not parsed: %+v", doc.Components)
	}
}

func TestParseBackwardCompat(t *testing.T) {
	const v101 = `arazzo: "1.0.1"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: api
    url: ./api.yaml
    type: openapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: op
        successCriteria:
          - condition: $statusCode == 200
`
	doc, err := NewParser().Parse(v101)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if doc.Arazzo != "1.0.1" || len(doc.Workflows) != 1 {
		t.Errorf("v1.0.1 doc not parsed correctly: %+v", doc)
	}
}
