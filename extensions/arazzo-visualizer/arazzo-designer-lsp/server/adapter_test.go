package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arazzo/lsp/parser"
	"github.com/arazzo/lsp/validator"
)

// Two AsyncAPI documents identical but for the one thing that decides the transport.
const busWithServers = `asyncapi: 3.0.0
info:
  title: Bus
  version: 1.0.0
servers:
  broker:
    host: broker.example.com
    protocol: mqtt
channels:
  orders:
    address: orders/new
`

const busWithoutServers = `asyncapi: 3.0.0
info:
  title: Bus
  version: 1.0.0
channels:
  orders:
    address: orders/new
`

const plainOpenAPI = `openapi: 3.0.0
info:
  title: REST
  version: 1.0.0
paths:
  /things:
    get:
      operationId: listThings
      responses:
        '200':
          description: ok
`

// An AsyncAPI source with no `servers` runs in-memory and contacts no broker — worth saying, because
// an in-memory send/receive always succeeds and so a forgotten `servers` produces a green run that
// proves nothing. A source that declares servers, and any OpenAPI source, must stay silent.
func TestSourceAdapterDiagnostic(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"with.asyncapi.yaml":    busWithServers,
		"without.asyncapi.yaml": busWithoutServers,
		"rest.openapi.yaml":     plainOpenAPI,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	arazzo := `arazzo: "1.1.0"
info:
  title: T
  version: "1.0.0"
sourceDescriptions:
  - name: brokered
    url: ./with.asyncapi.yaml
    type: asyncapi
  - name: localOnly
    url: ./without.asyncapi.yaml
    type: asyncapi
  - name: rest
    url: ./rest.openapi.yaml
    type: openapi
  - name: untyped
    url: ./without.asyncapi.yaml
workflows:
  - workflowId: wf
    steps:
      - stepId: await
        channelPath: localOnly#/channels/orders
        action: receive
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "wf.arazzo.yaml"), arazzo)

	doc, err := parser.NewParser().Parse(arazzo)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	errs := validator.NewValidator().
		WithSourceAdapterResolver(func(sd *parser.SourceDescription) (bool, bool) {
			return s.resolveSourceDeclaresServers(uri, arazzo, sd)
		}).
		Validate(doc)

	var dump strings.Builder
	for _, e := range errs {
		dump.WriteString("\n  [" + e.Severity + "] line " + itoa(e.Line) + " " + e.Message)
	}
	find := func(substr string) *validator.ValidationError {
		for i := range errs {
			if strings.Contains(errs[i].Message, substr) {
				return &errs[i]
			}
		}
		return nil
	}

	// The source with no servers is reported...
	got := find("'localOnly'")
	if got == nil {
		t.Fatalf("a source declaring no servers should be reported, got:%s", dump.String())
	}
	if got.Severity != "information" {
		t.Errorf("should be information, not %q — running in-memory is a supported mode", got.Severity)
	}
	if !strings.Contains(got.Message, "in-memory") || !strings.Contains(got.Message, "no broker is contacted") {
		t.Errorf("the message should name the consequence, got: %s", got.Message)
	}
	// ...on the line the entry starts on, so the marker lands on the source, not the top of the file.
	if got.Line == 0 {
		t.Errorf("expected the source description's own line, got 0 (marker would land at the top of the file)")
	}
	if !strings.Contains(strings.Split(arazzo, "\n")[got.Line], "localOnly") {
		t.Errorf("line %d is %q, expected the localOnly entry", got.Line, strings.Split(arazzo, "\n")[got.Line])
	}

	// A source that declares servers, and an OpenAPI source, are silent.
	if find("'brokered'") != nil {
		t.Errorf("a source declaring servers should be clean, got:%s", dump.String())
	}
	if find("'rest'") != nil {
		t.Errorf("an OpenAPI source has no servers concept and must be clean, got:%s", dump.String())
	}

	// `type` is OPTIONAL on a Source Description Object, so an untyped source pointing at an AsyncAPI
	// document with no servers must still be reported - the resolver decides from what the file is.
	if find("'untyped'") == nil {
		t.Errorf("an untyped source resolving to a serverless AsyncAPI doc should be reported, got:%s", dump.String())
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
