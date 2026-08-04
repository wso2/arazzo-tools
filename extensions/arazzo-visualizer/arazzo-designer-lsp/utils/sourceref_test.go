package utils

import (
	"reflect"
	"testing"
)

func TestNormalizeSourceRef(t *testing.T) {
	cases := []struct{ in, want string }{
		{"orderEvents", "orderEvents"},                             // bare name
		{"{$sourceDescriptions.orderEvents.url}", "orderEvents"},   // spec runtime-expression form
		{"$sourceDescriptions.orderEvents.url", "orderEvents"},     // same, unbraced
		{"{$sourceDescriptions.orderEvents}", "orderEvents"},       // lenient: no trailing field
		{" {$sourceDescriptions.orderEvents.url} ", "orderEvents"}, // surrounding whitespace
		{`"{$sourceDescriptions.orderEvents.url}"`, "orderEvents"}, // quoted
		{"{$sourceDescriptions.orderEvents.name}", "orderEvents"},  // any SD field, not just url
		{"", ""}, // empty stays empty
	}
	for _, c := range cases {
		if got := NormalizeSourceRef(c.in); got != c.want {
			t.Errorf("NormalizeSourceRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitSourceRefAndPointer(t *testing.T) {
	// Both source-reference spellings must yield the same source name and pointer.
	for _, in := range []string{
		"orderEvents#/channels/orders",
		"{$sourceDescriptions.orderEvents.url}#/channels/orders",
		`"{$sourceDescriptions.orderEvents.url}#/channels/orders"`,
	} {
		name, pointer, ok := SplitSourceRefAndPointer(in)
		if !ok || name != "orderEvents" || pointer != "/channels/orders" {
			t.Errorf("SplitSourceRefAndPointer(%q) = (%q,%q,%v)", in, name, pointer, ok)
		}
	}

	// Malformed values are rejected.
	for _, bad := range []string{"no-hash-here", "#/channels/orders", "orderEvents#", ""} {
		if _, _, ok := SplitSourceRefAndPointer(bad); ok {
			t.Errorf("SplitSourceRefAndPointer(%q) should be rejected", bad)
		}
	}
}

func TestIsRuntimeExpressionSourceRef(t *testing.T) {
	if !IsRuntimeExpressionSourceRef("{$sourceDescriptions.cat.url}") {
		t.Error("braced expression should be recognized as the runtime-expression form")
	}
	if IsRuntimeExpressionSourceRef("catalog") {
		t.Error("a bare name is not the runtime-expression form")
	}
}

func TestParseScopedOperationID(t *testing.T) {
	// Scoped form KEEPS the trailing segment (it's the operation id, not a source field).
	name, opID, ok := ParseScopedOperationID("$sourceDescriptions.orderEvents.placeOrder")
	if !ok || name != "orderEvents" || opID != "placeOrder" {
		t.Errorf("scoped form = (%q,%q,%v)", name, opID, ok)
	}
	if name, opID, ok := ParseScopedOperationID("{$sourceDescriptions.orderEvents.placeOrder}"); !ok || name != "orderEvents" || opID != "placeOrder" {
		t.Errorf("braced scoped form = (%q,%q,%v)", name, opID, ok)
	}
	// A bare operationId is not scoped.
	if _, _, ok := ParseScopedOperationID("placeOrder"); ok {
		t.Error("bare operationId should not parse as scoped")
	}
	// Malformed expressions.
	for _, bad := range []string{"$sourceDescriptions.onlyName", "$sourceDescriptions.", "$sourceDescriptions.x."} {
		if _, _, ok := ParseScopedOperationID(bad); ok {
			t.Errorf("%q should not parse as a scoped operationId", bad)
		}
	}
}

func TestSplitJSONPointer(t *testing.T) {
	// RFC 6901: ~1 decodes to '/', ~0 to '~', and ~01 must yield "~1" (not "/").
	cases := []struct {
		in   string
		want []string
	}{
		{"/paths/~1products/get", []string{"paths", "/products", "get"}},
		{"/paths/~1pet~1findByStatus/get", []string{"paths", "/pet/findByStatus", "get"}},
		{"/operations/placeOrder", []string{"operations", "placeOrder"}},
		{"/channels/orders", []string{"channels", "orders"}},
		{"/a~01b", []string{"a~1b"}},
		{"", nil},
		{"/", nil},
	}
	for _, c := range cases {
		if got := SplitJSONPointer(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitJSONPointer(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
