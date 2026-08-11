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

// The field name must be matched as a whole property key — not as a substring of a longer key, and
// not inside a comment.
func TestExtractFieldValueRequiresWholeKey(t *testing.T) {
	cases := []struct{ line, want string }{
		{"    channelPath: bus#/channels/orders", "bus#/channels/orders"},   // the key itself
		{"    - channelPath: bus#/channels/orders", "bus#/channels/orders"}, // sequence item
		{`    "channelPath": "bus#/channels/orders"`, "bus#/channels/orders"},
		{"    {channelPath: bus#/channels/orders}", "bus#/channels/orders"},               // flow mapping
		{"    - {stepId: s1, channelPath: bus#/channels/orders}", "bus#/channels/orders"}, // flow mapping in a sequence
		{"    x-channelPath: bus#/channels/orders", ""},                                   // extension key, not this field
		{"    # channelPath: bus#/channels/orders", ""},                                   // commented out
		{"    mychannelPath: bus#/channels/orders", ""},                                   // longer key ending in the field name
	}
	for _, c := range cases {
		if got := extractFieldValueAtPosition(c.line+"\n", protocol.Position{Line: 0}, "channelPath"); got != c.want {
			t.Errorf("%q -> got %q, want %q", c.line, got, c.want)
		}
	}
}
