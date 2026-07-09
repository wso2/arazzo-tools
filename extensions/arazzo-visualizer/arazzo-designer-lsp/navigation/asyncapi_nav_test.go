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
