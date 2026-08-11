// serializer.go is the Phase-10 message serialization layer. It separates a message's SHAPE
// (headers/payload the runtime reasons about) from its WIRE FORMAT (the bytes a broker actually
// carries). Steps build a logical payload; a Serializer turns it into bytes on send and back into a
// value on receive. Which serializer is used is chosen by content type through a SerializerRegistry,
// so adapters never reinvent encoding and new formats (Avro, Protobuf, ...) plug in by registration.
package executor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Serializer converts a logical payload to/from wire bytes for one family of content types.
type Serializer interface {
	// Name identifies the serializer for logs and errors (e.g. "json").
	Name() string
	// ContentType is the canonical content type this serializer produces (e.g. "application/json").
	ContentType() string
	// Serialize encodes a payload to bytes.
	Serialize(payload interface{}) ([]byte, error)
	// Deserialize decodes bytes back into a payload value.
	Deserialize(data []byte) (interface{}, error)
}

// SerializerRegistry picks a Serializer by content type. It is the single place that maps a wire
// format string to the code that handles it; register a new Serializer to support a new format.
type SerializerRegistry struct {
	byType   map[string]Serializer // normalized content type -> serializer
	fallback Serializer            // used when no content type is given
}

// NewDefaultSerializerRegistry returns a registry wired with the Phase-10 serializers: JSON (the
// default) and plain text are fully implemented; Protobuf and Avro are registered as clear
// "needs schema configuration" stubs so those content types fail with an explanatory error rather
// than being silently mis-encoded (they are completed alongside real brokers in Phase 11).
func NewDefaultSerializerRegistry() *SerializerRegistry {
	jsonSer := &JSONSerializer{}
	r := &SerializerRegistry{
		byType:   map[string]Serializer{},
		fallback: jsonSer,
	}
	r.Register(jsonSer)
	r.Register(&TextSerializer{})
	r.Register(newSchemaRequiredSerializer("protobuf", "application/x-protobuf"))
	r.Register(&aliasSerializer{contentType: "application/protobuf", target: r.mustGet("application/x-protobuf")})
	r.Register(newSchemaRequiredSerializer("avro", "application/avro"))
	r.Register(&aliasSerializer{contentType: "avro/binary", target: r.mustGet("application/avro")})
	return r
}

// Register adds (or overrides) the serializer for its canonical content type.
func (r *SerializerRegistry) Register(s Serializer) {
	r.byType[normalizeContentType(s.ContentType())] = s
}

// For returns the serializer for a content type. An empty content type uses the default (JSON); a
// `<something>+json` structured suffix maps to JSON; an unknown content type is a clear error so the
// runtime fails loudly instead of guessing the wire format.
func (r *SerializerRegistry) For(contentType string) (Serializer, error) {
	ct := normalizeContentType(contentType)
	if ct == "" {
		return r.fallback, nil
	}
	if s, ok := r.byType[ct]; ok {
		return s, nil
	}
	// A `<x>+json` structured suffix is JSON — but only if this registry actually has a JSON
	// serializer. Returning a missing entry would hand the caller a nil Serializer with a nil error,
	// which panics at the first method call instead of reporting the real problem.
	if strings.HasSuffix(ct, "+json") {
		if s, ok := r.byType["application/json"]; ok {
			return s, nil
		}
	}
	// Name the types that actually work separately from the ones that are only recognised. Listing a
	// stub as "supported" sends the reader off to try `application/avro`, which then fails with a
	// different error — the message would be pointing at a dead end.
	usable, stubbed := r.supported()
	if len(stubbed) == 0 {
		return nil, fmt.Errorf("no serializer registered for content type %q (supported: %s)", ct, strings.Join(usable, ", "))
	}
	return nil, fmt.Errorf("no serializer registered for content type %q (supported: %s; recognized but not yet implemented: %s)",
		ct, strings.Join(usable, ", "), strings.Join(stubbed, ", "))
}

// supported splits the registered content types (each sorted) into the ones that can actually encode
// and decode, and the ones that are only recognised — a stub selects cleanly but fails on use, so
// calling it "supported" in an error message would be misleading.
func (r *SerializerRegistry) supported() (usable, stubbed []string) {
	for ct, s := range r.byType {
		if isStub(s) {
			stubbed = append(stubbed, ct)
			continue
		}
		usable = append(usable, ct)
	}
	sort.Strings(usable)
	sort.Strings(stubbed)
	return usable, stubbed
}

// isStub reports whether a serializer is registered so its content type resolves and fails with an
// explanatory error, but cannot actually encode anything. An alias is whatever it points at.
func isStub(s Serializer) bool {
	if alias, ok := s.(*aliasSerializer); ok {
		s = alias.target
	}
	_, stub := s.(*schemaRequiredSerializer)
	return stub
}

// mustGet fetches an already-registered serializer by canonical content type (used to wire aliases).
// It panics if the target is missing: an alias registered before its target would otherwise wrap nil
// and fail much later, at the first message that uses that content type, with a nil dereference far
// from the cause. Callers are registry constructors in this package, so a panic here is a programming
// error caught on the first run, never something a document can trigger.
func (r *SerializerRegistry) mustGet(contentType string) Serializer {
	s, ok := r.byType[normalizeContentType(contentType)]
	if !ok {
		panic(fmt.Sprintf("serializer registry: alias target %q is not registered yet", contentType))
	}
	return s
}

// normalizeContentType lowercases, trims, and drops any parameters (e.g. "; charset=utf-8") so
// "application/json; charset=utf-8" and "application/json" select the same serializer.
func normalizeContentType(contentType string) string {
	ct := strings.TrimSpace(strings.ToLower(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

// --- JSON (default) ---

// JSONSerializer encodes/decodes application/json payloads.
type JSONSerializer struct{}

func (*JSONSerializer) Name() string        { return "json" }
func (*JSONSerializer) ContentType() string { return "application/json" }

func (*JSONSerializer) Serialize(payload interface{}) ([]byte, error) {
	return json.Marshal(payload)
}

func (*JSONSerializer) Deserialize(data []byte) (interface{}, error) {
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil // an empty body decodes to no payload rather than a JSON error
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("invalid JSON message body: %w", err)
	}
	return v, nil
}

// --- text/plain ---

// TextSerializer carries payloads as raw UTF-8 text. Non-string payloads are stringified on send;
// received bytes are surfaced as a Go string.
type TextSerializer struct{}

func (*TextSerializer) Name() string        { return "text" }
func (*TextSerializer) ContentType() string { return "text/plain" }

func (*TextSerializer) Serialize(payload interface{}) ([]byte, error) {
	switch p := payload.(type) {
	case nil:
		return []byte{}, nil
	case string:
		return []byte(p), nil
	case []byte:
		return p, nil
	case map[string]interface{}, map[interface{}]interface{}, []interface{}:
		// A structured payload has no meaningful plain-text form. Stringifying it would put Go's own
		// map/slice rendering ("map[kind:deploy]") on the wire — unreadable to any consumer and
		// silently wrong. Failing here matches how the registry treats an unknown content type: never
		// guess a wire format. This is reachable without the author writing text/plain themselves,
		// since an AsyncAPI channel's declared contentType selects the serializer for them.
		return nil, fmt.Errorf("cannot serialize a structured payload (object/array) as text/plain: declare a structured contentType such as application/json on the step's requestBody, or send a scalar value")
	default:
		// Scalars (numbers, booleans) have an unambiguous text form.
		return []byte(fmt.Sprintf("%v", p)), nil
	}
}

func (*TextSerializer) Deserialize(data []byte) (interface{}, error) {
	return string(data), nil
}

// --- schema-dependent stubs (Protobuf, Avro) ---

// schemaRequiredSerializer is a placeholder for a binary format that cannot encode/decode without
// external schema configuration. It selects cleanly from the registry but fails with an explanatory
// error on use, documenting the Phase-11 expectation instead of silently mis-encoding a payload.
type schemaRequiredSerializer struct {
	name        string
	contentType string
}

func newSchemaRequiredSerializer(name, contentType string) *schemaRequiredSerializer {
	return &schemaRequiredSerializer{name: name, contentType: contentType}
}

func (s *schemaRequiredSerializer) Name() string        { return s.name }
func (s *schemaRequiredSerializer) ContentType() string { return s.contentType }

func (s *schemaRequiredSerializer) Serialize(interface{}) ([]byte, error) {
	return nil, s.unsupported()
}

func (s *schemaRequiredSerializer) Deserialize([]byte) (interface{}, error) {
	return nil, s.unsupported()
}

func (s *schemaRequiredSerializer) unsupported() error {
	return fmt.Errorf("%s serialization (%s) is not supported yet", s.name, s.contentType)
}

// aliasSerializer maps an additional content type onto an already-registered serializer (e.g.
// "application/protobuf" -> the "application/x-protobuf" serializer) without duplicating logic.
type aliasSerializer struct {
	contentType string
	target      Serializer
}

func (a *aliasSerializer) Name() string        { return a.target.Name() }
func (a *aliasSerializer) ContentType() string { return a.contentType }

func (a *aliasSerializer) Serialize(payload interface{}) ([]byte, error) {
	return a.target.Serialize(payload)
}
func (a *aliasSerializer) Deserialize(data []byte) (interface{}, error) {
	return a.target.Deserialize(data)
}
