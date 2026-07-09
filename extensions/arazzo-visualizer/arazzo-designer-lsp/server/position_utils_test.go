package server

import (
	"testing"

	"go.lsp.dev/protocol"
)

func TestExtractChannelKeyAtPosition(t *testing.T) {
	content := "steps:\n" +
		"  - stepId: s1\n" +
		"    channelPath: orderBus#/channels/orders\n" +
		"    action: send\n"

	// line 2 is the channelPath line
	if got := extractChannelKeyAtPosition(content, protocol.Position{Line: 2}); got != "orders" {
		t.Errorf("channelPath line: got %q, want %q", got, "orders")
	}
	// a non-channelPath line yields nothing
	if got := extractChannelKeyAtPosition(content, protocol.Position{Line: 1}); got != "" {
		t.Errorf("non-channelPath line: got %q, want empty", got)
	}
	// quoted value form
	q := "    channelPath: \"bus#/channels/confirmations\"\n"
	if got := extractChannelKeyAtPosition(q, protocol.Position{Line: 0}); got != "confirmations" {
		t.Errorf("quoted channelPath: got %q, want %q", got, "confirmations")
	}
}
