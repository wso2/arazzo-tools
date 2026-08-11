package navigation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arazzo/lsp/utils"
)

func TestParseAsyncAPIFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.asyncapi.yaml")
	content := `asyncapi: 3.0.0
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
  onConfirmed:
    action: receive
    channel:
      $ref: '#/channels/confirmations'
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := ParseOpenAPIFile(utils.PathToURI(path))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(f.Operations) != 2 {
		t.Errorf("expected 2 async operations, got %d", len(f.Operations))
	}
	if len(f.Channels) != 2 {
		t.Errorf("expected 2 channels, got %d", len(f.Channels))
	}

	// Index and look up (mirrors what the indexer + definition handler do).
	idx := NewOperationIndex()
	for _, op := range f.Operations {
		idx.AddOperation(op)
	}
	for _, ch := range f.Channels {
		idx.AddChannel(ch)
	}

	if op, ok := idx.Lookup("placeOrder"); !ok || op.Method != "SEND" {
		t.Errorf("placeOrder lookup: op=%+v ok=%v (want Method SEND)", op, ok)
	}
	if op, ok := idx.Lookup("onConfirmed"); !ok || op.Method != "RECEIVE" {
		t.Errorf("onConfirmed lookup: op=%+v ok=%v (want Method RECEIVE)", op, ok)
	}
	ch, ok := idx.LookupChannel("orders")
	if !ok || ch.Address != "orders/new" || ch.LineNumber != 5 {
		t.Errorf("orders channel lookup: ch=%+v ok=%v (want address orders/new, line 5)", ch, ok)
	}
}

// The content type an AsyncAPI document declares is resolved at index time, following AsyncAPI's own
// precedence (a message's own contentType, else the document's root defaultContentType) — and an
// operation records the channel it targets, so an operationId step can reach the same declaration a
// channelPath step reaches directly.
func TestParseAsyncAPIContentTypeAndChannelLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.asyncapi.yaml")
	content := `asyncapi: 3.0.0
info:
  title: Order Events
  version: 1.0.0
defaultContentType: application/json
channels:
  orders:
    address: orders/new
    messages:
      order:
        contentType: text/plain
  confirmations:
    address: orders/confirmed
  plain:
    address: plain/topic
operations:
  placeOrder:
    action: send
    channel:
      $ref: '#/channels/orders'
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := ParseOpenAPIFile(utils.PathToURI(path))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	byKey := map[string]*ChannelInfo{}
	for _, ch := range f.Channels {
		byKey[ch.Key] = ch
	}

	// A message's own contentType wins.
	if got := byKey["orders"].ContentType; got != "text/plain" {
		t.Errorf("orders: message contentType should win, got %q", got)
	}
	// No message declaration -> the document's root defaultContentType applies (AsyncAPI 3.0 MUST).
	if got := byKey["confirmations"].ContentType; got != "application/json" {
		t.Errorf("confirmations: should fall back to defaultContentType, got %q", got)
	}
	if got := byKey["plain"].ContentType; got != "application/json" {
		t.Errorf("plain: should fall back to defaultContentType, got %q", got)
	}

	// The operation knows which channel it targets.
	if f.Operations[0].ChannelKey != "orders" {
		t.Errorf("placeOrder should record its channel key, got %q", f.Operations[0].ChannelKey)
	}
}

// With no defaultContentType and no message declaration, the channel declares nothing — which is the
// case that lets the editor say "this will be serialized as JSON".
func TestParseAsyncAPINoContentTypeDeclared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bare.asyncapi.yaml")
	content := `asyncapi: 3.0.0
info:
  title: Bare
  version: 1.0.0
channels:
  orders:
    address: orders/new
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ParseOpenAPIFile(utils.PathToURI(path))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := f.Channels[0].ContentType; got != "" {
		t.Errorf("a document declaring nothing should resolve to an empty content type, got %q", got)
	}
}
