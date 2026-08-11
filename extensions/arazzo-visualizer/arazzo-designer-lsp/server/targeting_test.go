package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arazzo/lsp/utils"
	"go.lsp.dev/protocol"
)

// The three ways an Arazzo step targets something — operationId (bare/scoped), channelPath, and
// operationPath — must all navigate to the right FILE and LINE, must accept a source description
// written either as a bare name or as the spec's "{$sourceDescriptions.<name>.url}" runtime
// expression, and hover must agree with Go-to-Definition in every case.

const catalogSpec = `openapi: 3.0.3
info:
  title: Catalog API
  version: 1.0.0
paths:
  /products:
    get:
      operationId: getProducts
      responses:
        '200':
          description: OK
`

const orderEventsSpec = `asyncapi: 3.0.0
info:
  title: Order Events
  version: 1.0.0
channels:
  orders:
    address: orders/new
  confirmations:
    address: orders/confirmed
operations:
  placeOrder:
    action: send
    channel:
      $ref: '#/channels/orders'
  onOrderConfirmed:
    action: receive
    channel:
      $ref: '#/channels/confirmations'
`

// writeSpecs drops the two source specs into dir and returns their paths.
func writeSpecs(t *testing.T, dir string) (catalog, orders string) {
	t.Helper()
	catalog = filepath.Join(dir, "catalog.openapi.yaml")
	orders = filepath.Join(dir, "order-events.asyncapi.yaml")
	if err := os.WriteFile(catalog, []byte(catalogSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orders, []byte(orderEventsSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	return catalog, orders
}

// openDoc registers an Arazzo document with the server exactly as DidOpen would, without the
// asynchronous indexing goroutine (navigation indexes on demand, so this stays deterministic).
func openDoc(t *testing.T, s *Server, path, content string) protocol.DocumentURI {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := protocol.DocumentURI(utils.PathToURI(path))
	s.documents[uri] = content
	return uri
}

// lineOf returns the 0-based line index of the first line containing substr.
func lineOf(t *testing.T, content, substr string) uint32 {
	t.Helper()
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, substr) {
			return uint32(i)
		}
	}
	t.Fatalf("no line containing %q", substr)
	return 0
}

// definitionAt runs Go-to-Definition at the line holding substr and returns the single location.
func definitionAt(t *testing.T, s *Server, uri protocol.DocumentURI, content, substr string) protocol.Location {
	t.Helper()
	locs, err := s.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: lineOf(t, content, substr)},
		},
	})
	if err != nil {
		t.Fatalf("definition(%s): %v", substr, err)
	}
	if len(locs) != 1 {
		t.Fatalf("definition(%s): expected 1 location, got %d", substr, len(locs))
	}
	return locs[0]
}

// hoverAt runs Hover at the line holding substr and returns its markdown ("" when there is none).
func hoverAt(t *testing.T, s *Server, uri protocol.DocumentURI, content, substr string) string {
	t.Helper()
	h, err := s.Hover(context.Background(), &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: lineOf(t, content, substr)},
		},
	})
	if err != nil || h == nil {
		return ""
	}
	return h.Contents.Value
}

func expectFileLine(t *testing.T, got protocol.Location, wantPath string, wantLine uint32, what string) {
	t.Helper()
	wantURI := utils.PathToURI(wantPath)
	if string(got.URI) != wantURI {
		t.Errorf("%s: resolved to %s, want %s", what, got.URI, wantURI)
	}
	if got.Range.Start.Line != wantLine {
		t.Errorf("%s: resolved to line %d, want %d", what, got.Range.Start.Line, wantLine)
	}
}

// TestTargetingFormsNavigate covers every targeting form, with the source description written both
// as a bare name and as the spec's runtime-expression form.
func TestTargetingFormsNavigate(t *testing.T) {
	dir := t.TempDir()
	catalog, orders := writeSpecs(t, dir)

	arazzo := `arazzo: "1.1.0"
info:
  title: Targeting
  version: "1.0.0"
sourceDescriptions:
  - name: catalog
    url: ./catalog.openapi.yaml
    type: openapi
  - name: orderEvents
    url: ./order-events.asyncapi.yaml
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: bareOperationId
        operationId: getProducts
      - stepId: scopedOperationId
        operationId: $sourceDescriptions.orderEvents.placeOrder
      - stepId: bareChannelPath
        channelPath: orderEvents#/channels/orders
        action: send
      - stepId: exprChannelPath
        channelPath: '{$sourceDescriptions.orderEvents.url}#/channels/confirmations'
        action: receive
      - stepId: bareOperationPath
        operationPath: catalog#/paths/~1products/get
      - stepId: exprOperationPath
        operationPath: '{$sourceDescriptions.catalog.url}#/paths/~1products/get'
      - stepId: asyncOperationPath
        operationPath: 'orderEvents#/operations/onOrderConfirmed'
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "flow.arazzo.yaml"), arazzo)

	// Expected definition lines in the source specs.
	getProductsLine := lineOf(t, catalogSpec, "operationId: getProducts")
	placeOrderLine := lineOf(t, orderEventsSpec, "  placeOrder:")
	onConfirmedLine := lineOf(t, orderEventsSpec, "  onOrderConfirmed:")
	ordersChannelLine := lineOf(t, orderEventsSpec, "  orders:")
	confirmationsChannelLine := lineOf(t, orderEventsSpec, "  confirmations:")

	cases := []struct {
		marker   string
		wantFile string
		wantLine uint32
	}{
		{"operationId: getProducts", catalog, getProductsLine},
		{"operationId: $sourceDescriptions.orderEvents.placeOrder", orders, placeOrderLine},
		{"channelPath: orderEvents#/channels/orders", orders, ordersChannelLine},
		{"channelPath: '{$sourceDescriptions.orderEvents.url}#/channels/confirmations'", orders, confirmationsChannelLine},
		{"operationPath: catalog#/paths/~1products/get", catalog, getProductsLine},
		{"operationPath: '{$sourceDescriptions.catalog.url}#/paths/~1products/get'", catalog, getProductsLine},
		{"operationPath: 'orderEvents#/operations/onOrderConfirmed'", orders, onConfirmedLine},
	}
	for _, c := range cases {
		loc := definitionAt(t, s, uri, arazzo, c.marker)
		expectFileLine(t, loc, c.wantFile, c.wantLine, c.marker)

		// Hover must describe the SAME file Go-to-Definition jumps to.
		md := hoverAt(t, s, uri, arazzo, c.marker)
		if md == "" {
			t.Errorf("%s: expected hover content", c.marker)
			continue
		}
		if !strings.Contains(md, filepath.Base(c.wantFile)) {
			t.Errorf("%s: hover names a different file than definition:\n%s", c.marker, md)
		}
	}
}

// TestPhase8ExampleNavigates drives the REAL example file a reviewer will Ctrl+click through, so the
// shipped example is guaranteed to behave as its comments claim.
func TestPhase8ExampleNavigates(t *testing.T) {
	exampleDir := filepath.Join("..", "..", "..", "..", "examples", "async_test", "phase8")
	path, err := filepath.Abs(filepath.Join(exampleDir, "04-step-targeting-forms.arazzo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("example not found: %v", err)
	}
	content := string(raw)

	s := NewServer()
	uri := protocol.DocumentURI(utils.PathToURI(path))
	s.documents[uri] = content

	// Every targeting line in the example, with the file it must land in.
	cases := []struct{ marker, wantFile string }{
		{"operationId: getProducts", "catalog.openapi.yaml"},
		{"operationId: $sourceDescriptions.orderEvents.placeOrder", "order-events.asyncapi.yaml"},
		{"channelPath: orderEvents#/channels/orders", "order-events.asyncapi.yaml"},
		{"channelPath: '{$sourceDescriptions.orderEvents.url}#/channels/orders'", "order-events.asyncapi.yaml"},
		{"operationPath: catalog#/paths/~1products/get", "catalog.openapi.yaml"},
		{"operationPath: '{$sourceDescriptions.catalog.url}#/paths/~1products/get'", "catalog.openapi.yaml"},
		{"operationPath: 'orderEvents#/operations/onOrderConfirmed'", "order-events.asyncapi.yaml"},
	}
	for _, c := range cases {
		loc := definitionAt(t, s, uri, content, c.marker)
		if !strings.HasSuffix(string(loc.URI), c.wantFile) {
			t.Errorf("%s: jumped to %s, want %s", c.marker, loc.URI, c.wantFile)
		}
		if md := hoverAt(t, s, uri, content, c.marker); !strings.Contains(md, c.wantFile) {
			t.Errorf("%s: hover names a different file than the jump target:\n%s", c.marker, md)
		}
	}

	// The example declares one AsyncAPI and one OpenAPI source; the registry must separate them.
	s.indexDeclaredSources(uri, content)
	if async := s.AsyncSources(uri); len(async) != 1 || async[0].Name != "orderEvents" {
		t.Errorf("AsyncSources = %+v, want just orderEvents", async)
	}
	if rest := s.RESTSources(uri); len(rest) != 1 || rest[0].Name != "catalog" {
		t.Errorf("RESTSources = %+v, want just catalog", rest)
	}
}

// A reference must resolve ONLY inside the sources the document declares — never into a same-named
// operation sitting in an undeclared file elsewhere.
func TestNavigationScopedToDeclaredSources(t *testing.T) {
	dir := t.TempDir()
	catalog, _ := writeSpecs(t, dir)

	// An undeclared spec that also defines getProducts, in the same folder.
	other := filepath.Join(dir, "other.openapi.yaml")
	if err := os.WriteFile(other, []byte(catalogSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	arazzo := `arazzo: "1.1.0"
info:
  title: Scoped
  version: "1.0.0"
sourceDescriptions:
  - name: catalog
    url: ./catalog.openapi.yaml
    type: openapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: getProducts
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "flow.arazzo.yaml"), arazzo)

	loc := definitionAt(t, s, uri, arazzo, "operationId: getProducts")
	expectFileLine(t, loc, catalog, lineOf(t, catalogSpec, "operationId: getProducts"), "declared-source scoping")
	if strings.Contains(string(loc.URI), "other.openapi.yaml") {
		t.Error("resolved into an undeclared source file")
	}

	// A channelPath naming a source this document never declared resolves to nothing.
	arazzo2 := strings.Replace(arazzo,
		"        operationId: getProducts\n",
		"        channelPath: ghostBus#/channels/orders\n        action: send\n", 1)
	uri2 := openDoc(t, s, filepath.Join(dir, "flow2.arazzo.yaml"), arazzo2)
	locs, err := s.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri2},
			Position:     protocol.Position{Line: lineOf(t, arazzo2, "channelPath: ghostBus")},
		},
	})
	if err != nil || len(locs) != 0 {
		t.Errorf("undeclared source should resolve to nothing, got %v (%v)", locs, err)
	}
}

// A source NAME is only meaningful inside the document that declared it. Two Arazzo documents may
// each declare "orderEvents" pointing at different files (with identically-named channels and
// operations inside) — each document must resolve to ITS OWN file. Normalizing a source reference
// down to its name therefore loses nothing: the name is resolved per-document, never globally.
func TestSameSourceNameInTwoDocumentsStaysSeparate(t *testing.T) {
	dir := t.TempDir()

	// Two different AsyncAPI files, same channel key and same operation id in both.
	busA := filepath.Join(dir, "bus-a.asyncapi.yaml")
	busB := filepath.Join(dir, "bus-b.asyncapi.yaml")
	for _, f := range []string{busA, busB} {
		if err := os.WriteFile(f, []byte(orderEventsSpec), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Both documents call their source "orderEvents" — but point it at different files, and write
	// the reference in different (equally legal) spellings.
	docFor := func(url, channelRef string) string {
		return `arazzo: "1.1.0"
info:
  title: Same Name
  version: "1.0.0"
sourceDescriptions:
  - name: orderEvents
    url: ` + url + `
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        channelPath: ` + channelRef + `
        action: send
      - stepId: s2
        operationId: $sourceDescriptions.orderEvents.placeOrder
`
	}
	contentA := docFor("./bus-a.asyncapi.yaml", "orderEvents#/channels/orders")
	contentB := docFor("./bus-b.asyncapi.yaml", "'{$sourceDescriptions.orderEvents.url}#/channels/orders'")

	s := NewServer()
	uriA := openDoc(t, s, filepath.Join(dir, "flow-a.arazzo.yaml"), contentA)
	uriB := openDoc(t, s, filepath.Join(dir, "flow-b.arazzo.yaml"), contentB)

	// Same source name, same channel key, same operation id — but each document resolves to its own
	// file, regardless of which spelling it used.
	if loc := definitionAt(t, s, uriA, contentA, "channelPath:"); string(loc.URI) != utils.PathToURI(busA) {
		t.Errorf("doc A channelPath resolved to %s, want bus-a", loc.URI)
	}
	if loc := definitionAt(t, s, uriB, contentB, "channelPath:"); string(loc.URI) != utils.PathToURI(busB) {
		t.Errorf("doc B channelPath resolved to %s, want bus-b", loc.URI)
	}
	if loc := definitionAt(t, s, uriA, contentA, "operationId:"); string(loc.URI) != utils.PathToURI(busA) {
		t.Errorf("doc A operationId resolved to %s, want bus-a", loc.URI)
	}
	if loc := definitionAt(t, s, uriB, contentB, "operationId:"); string(loc.URI) != utils.PathToURI(busB) {
		t.Errorf("doc B operationId resolved to %s, want bus-b", loc.URI)
	}

	// The registry keeps the two entries apart as well.
	s.indexDeclaredSources(uriA, contentA)
	s.indexDeclaredSources(uriB, contentB)
	srcA, okA := s.sourceRegistry.lookup(uriA, "orderEvents")
	srcB, okB := s.sourceRegistry.lookup(uriB, "orderEvents")
	if !okA || !okB || srcA.FileURI == srcB.FileURI {
		t.Errorf("same name in two documents must map to different files: A=%+v B=%+v", srcA, srcB)
	}
}

// Relative source URLs must resolve against the base URI derived from `$self` (spec §5.5) — the same
// rule the runner uses — so navigation follows a document that declares a different canonical home.
func TestSelfAwareSourceResolution(t *testing.T) {
	root := t.TempDir()
	specsDir := filepath.Join(root, "specs")
	flowsDir := filepath.Join(root, "flows")
	for _, d := range []string{specsDir, flowsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	catalog, _ := writeSpecs(t, specsDir)

	// The document lives in flows/ but declares itself as living in specs/, so "./catalog.openapi.yaml"
	// must resolve into specs/ — not next to the file itself.
	arazzo := `arazzo: "1.1.0"
$self: ../specs/flow.arazzo.yaml
info:
  title: Self
  version: "1.0.0"
sourceDescriptions:
  - name: catalog
    url: ./catalog.openapi.yaml
    type: openapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: getProducts
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(flowsDir, "flow.arazzo.yaml"), arazzo)

	loc := definitionAt(t, s, uri, arazzo, "operationId: getProducts")
	expectFileLine(t, loc, catalog, lineOf(t, catalogSpec, "operationId: getProducts"), "$self-based resolution")
}

// The per-document registry must record each declared source's name, declared type, resolved type
// and file, and keep event-driven sources separate from REST ones.
func TestSourceRegistryTracksTypesPerDocument(t *testing.T) {
	dir := t.TempDir()
	catalog, orders := writeSpecs(t, dir)

	arazzo := `arazzo: "1.1.0"
info:
  title: Registry
  version: "1.0.0"
sourceDescriptions:
  - name: catalog
    url: ./catalog.openapi.yaml
    type: openapi
  - name: orderEvents
    url: ./order-events.asyncapi.yaml
    type: asyncapi
  - name: remote
    url: https://example.com/remote.openapi.yaml
    type: openapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: getProducts
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "flow.arazzo.yaml"), arazzo)
	s.indexDeclaredSources(uri, arazzo)

	all := s.DocumentSources(uri)
	if len(all) != 3 {
		t.Fatalf("expected 3 registered sources, got %d", len(all))
	}

	byName := map[string]DocumentSource{}
	for _, src := range all {
		byName[src.Name] = src
	}

	if got := byName["catalog"]; got.DeclaredType != SourceTypeOpenAPI ||
		got.ResolvedType != SourceTypeOpenAPI || got.FileURI != utils.PathToURI(catalog) || got.Remote {
		t.Errorf("catalog source registered wrong: %+v", got)
	}
	if got := byName["orderEvents"]; got.DeclaredType != SourceTypeAsyncAPI ||
		got.ResolvedType != SourceTypeAsyncAPI || got.FileURI != utils.PathToURI(orders) || !got.IsAsync() {
		t.Errorf("orderEvents source registered wrong: %+v", got)
	}
	if got := byName["remote"]; !got.Remote || got.FileURI != "" {
		t.Errorf("remote source should be flagged remote with no file: %+v", got)
	}

	// Async and REST must be reported separately.
	async := s.AsyncSources(uri)
	if len(async) != 1 || async[0].Name != "orderEvents" {
		t.Errorf("AsyncSources = %+v, want just orderEvents", async)
	}
	rest := s.RESTSources(uri)
	if len(rest) != 2 {
		t.Errorf("RESTSources = %+v, want catalog + remote", rest)
	}

	// The custom request exposes the same split to clients (e.g. the graph).
	info := s.buildSourceInfo(uri)
	if len(info.Sources) != 3 || len(info.Async) != 1 || len(info.REST) != 2 {
		t.Errorf("buildSourceInfo = %d sources / %d async / %d rest", len(info.Sources), len(info.Async), len(info.REST))
	}

	// Closing the document forgets its sources (names are document-scoped).
	if err := s.DidClose(context.Background(), &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}); err != nil {
		t.Fatalf("didClose: %v", err)
	}
	if len(s.DocumentSources(uri)) != 0 {
		t.Error("closing a document should drop its source registry entry")
	}
}

// A source whose file contradicts its declared `type` is detectable via the registry.
func TestSourceRegistryDetectsTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	writeSpecs(t, dir)

	arazzo := `arazzo: "1.1.0"
info:
  title: Mismatch
  version: "1.0.0"
sourceDescriptions:
  - name: bus
    url: ./catalog.openapi.yaml
    type: asyncapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: getProducts
`
	s := NewServer()
	uri := openDoc(t, s, filepath.Join(dir, "flow.arazzo.yaml"), arazzo)
	s.indexDeclaredSources(uri, arazzo)

	sources := s.DocumentSources(uri)
	if len(sources) != 1 {
		t.Fatalf("expected 1 source, got %d", len(sources))
	}
	if !sources[0].TypeMismatch() {
		t.Errorf("declared asyncapi over an openapi file should be a type mismatch: %+v", sources[0])
	}
}

// Saving an Arazzo document must re-resolve and re-index its declared sources, so a source added
// while the file was open becomes navigable without reopening it.
func TestSaveReindexesDeclaredSources(t *testing.T) {
	dir := t.TempDir()
	_, orders := writeSpecs(t, dir)

	before := `arazzo: "1.1.0"
info:
  title: Save
  version: "1.0.0"
sourceDescriptions:
  - name: catalog
    url: ./catalog.openapi.yaml
    type: openapi
workflows:
  - workflowId: wf
    steps:
      - stepId: s1
        operationId: getProducts
`
	s := NewServer()
	path := filepath.Join(dir, "flow.arazzo.yaml")
	uri := openDoc(t, s, path, before)
	s.indexDeclaredSources(uri, before)

	if len(s.AsyncSources(uri)) != 0 {
		t.Fatal("no asyncapi source declared yet")
	}

	// The author adds an AsyncAPI source and saves.
	after := strings.Replace(before,
		"    type: openapi\n",
		"    type: openapi\n  - name: orderEvents\n    url: ./order-events.asyncapi.yaml\n    type: asyncapi\n", 1)
	if err := os.WriteFile(path, []byte(after), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.DidSave(context.Background(), &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
		Text:         after,
	}); err != nil {
		t.Fatalf("didSave: %v", err)
	}
	// DidSave indexes in the background; resolve synchronously to assert the outcome deterministically.
	s.indexDeclaredSources(uri, after)

	async := s.AsyncSources(uri)
	if len(async) != 1 || async[0].FileURI != utils.PathToURI(orders) {
		t.Fatalf("after save the AsyncAPI source should be registered, got %+v", async)
	}
}
