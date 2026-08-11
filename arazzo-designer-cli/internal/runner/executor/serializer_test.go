package executor

import (
	"strings"
	"testing"
)

func TestSerializerRegistry_Selection(t *testing.T) {
	r := NewDefaultSerializerRegistry()

	cases := []struct {
		contentType string
		wantName    string
		wantErr     bool
	}{
		{"", "json", false},                                // empty -> default JSON
		{"application/json", "json", false},                // exact
		{"application/json; charset=utf-8", "json", false}, // parameters stripped
		{"APPLICATION/JSON", "json", false},                // case-insensitive
		{"text/plain", "text", false},                      // text
		{"application/cloudevents+json", "json", false},    // structured +json suffix
		{"application/x-protobuf", "protobuf", false},      // known-but-stubbed still selects
		{"application/avro", "avro", false},
		{"application/octet-stream", "", true}, // unknown -> clear error
	}
	for _, c := range cases {
		s, err := r.For(c.contentType)
		if c.wantErr {
			if err == nil {
				t.Errorf("For(%q): expected error, got serializer %q", c.contentType, s.Name())
			}
			continue
		}
		if err != nil {
			t.Errorf("For(%q): unexpected error %v", c.contentType, err)
			continue
		}
		if s.Name() != c.wantName {
			t.Errorf("For(%q): got serializer %q, want %q", c.contentType, s.Name(), c.wantName)
		}
	}
}

func TestSerializerRegistry_UnknownErrorListsSupported(t *testing.T) {
	r := NewDefaultSerializerRegistry()
	_, err := r.For("application/octet-stream")
	if err == nil || !strings.Contains(err.Error(), "application/json") {
		t.Fatalf("unknown content type error should list supported types, got: %v", err)
	}
}

func TestJSONSerializer_RoundTrip(t *testing.T) {
	s := &JSONSerializer{}
	in := map[string]interface{}{"orderId": "A1", "status": "new"}

	raw, err := s.Serialize(in)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if !strings.Contains(string(raw), `"orderId"`) {
		t.Errorf("serialized JSON missing field: %s", raw)
	}

	out, err := s.Deserialize(raw)
	if err != nil {
		t.Fatalf("deserialize: %v", err)
	}
	m, ok := out.(map[string]interface{})
	if !ok || m["orderId"] != "A1" || m["status"] != "new" {
		t.Errorf("round-trip mismatch: got %#v", out)
	}
}

func TestJSONSerializer_EmptyBodyIsNil(t *testing.T) {
	s := &JSONSerializer{}
	v, err := s.Deserialize([]byte("   "))
	if err != nil || v != nil {
		t.Errorf("empty body should deserialize to (nil, nil), got (%v, %v)", v, err)
	}
}

func TestJSONSerializer_InvalidBodyErrors(t *testing.T) {
	s := &JSONSerializer{}
	if _, err := s.Deserialize([]byte("{not json")); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestTextSerializer_RoundTrip(t *testing.T) {
	s := &TextSerializer{}

	raw, err := s.Serialize("hello")
	if err != nil || string(raw) != "hello" {
		t.Fatalf("serialize string: %q / %v", raw, err)
	}
	// non-string payloads are stringified
	raw2, _ := s.Serialize(42)
	if string(raw2) != "42" {
		t.Errorf("serialize int: got %q, want 42", raw2)
	}
	out, err := s.Deserialize([]byte("world"))
	if err != nil || out != "world" {
		t.Errorf("deserialize: got %v / %v", out, err)
	}
}

func TestSchemaRequiredSerializers_FailClearly(t *testing.T) {
	r := NewDefaultSerializerRegistry()
	for _, ct := range []string{"application/x-protobuf", "application/protobuf", "application/avro", "avro/binary"} {
		s, err := r.For(ct)
		if err != nil {
			t.Fatalf("For(%q) should select a stub serializer, got err %v", ct, err)
		}
		if _, err := s.Serialize(map[string]interface{}{"x": 1}); err == nil {
			t.Errorf("%q serialize should fail with a needs-schema error", ct)
		}
		if _, err := s.Deserialize([]byte{0x01}); err == nil {
			t.Errorf("%q deserialize should fail with a needs-schema error", ct)
		}
	}
}

// A structured payload has no plain-text form. Stringifying it would put Go's own map rendering on
// the wire, which no consumer can read — and this is reachable without the author asking for
// text/plain, because an AsyncAPI channel's declared contentType selects the serializer for them.
func TestTextSerializer_RejectsStructuredPayloads(t *testing.T) {
	s := &TextSerializer{}
	for _, payload := range []interface{}{
		map[string]interface{}{"kind": "deploy"},
		[]interface{}{1, 2, 3},
		map[interface{}]interface{}{"k": "v"},
	} {
		raw, err := s.Serialize(payload)
		if err == nil {
			t.Errorf("serializing %T as text/plain should fail, got %q", payload, raw)
		}
	}

	// Scalars still work — they have an unambiguous text form.
	for _, payload := range []interface{}{"hi", 42, true, nil} {
		if _, err := s.Serialize(payload); err != nil {
			t.Errorf("serializing scalar %v as text/plain should succeed, got %v", payload, err)
		}
	}
}

// A registry without a JSON serializer must report a `+json` content type as unsupported rather than
// returning a nil Serializer with a nil error, which would panic at the first method call.
func TestRegistryWithoutJSONReportsSuffixAsUnsupported(t *testing.T) {
	r := &SerializerRegistry{byType: map[string]Serializer{}, fallback: &TextSerializer{}}
	r.Register(&TextSerializer{})

	s, err := r.For("application/vnd.order+json")
	if err == nil {
		t.Fatalf("expected an error, got serializer %v", s)
	}
	if s != nil {
		t.Errorf("a failed lookup must return a nil serializer alongside the error, got %v", s)
	}
}
