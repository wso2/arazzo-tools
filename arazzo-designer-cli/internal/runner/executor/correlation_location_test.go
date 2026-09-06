package executor

import (
	"encoding/json"
	"testing"
	"time"
)

// correlationSources builds an AsyncAPI document exercising every shape a Correlation ID Object can
// take: declared inline, reached through a $ref'd message, reached through a $ref'd Correlation ID
// Object, declared per-message on a multi-kind channel, and absent entirely.
func correlationSources() map[string]interface{} {
	spec := map[string]interface{}{
		"asyncapi": "3.0.0",
		"channels": map[string]interface{}{
			// location declared inline on the message
			"orders": map[string]interface{}{
				"address": "orders/new",
				"messages": map[string]interface{}{
					"order": map[string]interface{}{
						"contentType":   "application/json",
						"correlationId": map[string]interface{}{"location": "$message.header#/correlationId"},
					},
				},
			},
			// the MESSAGE is a $ref; the location lives in components
			"audits": map[string]interface{}{
				"address":  "audit/log",
				"messages": map[string]interface{}{"audit": map[string]interface{}{"$ref": "#/components/messages/audit"}},
			},
			// the CORRELATION ID OBJECT itself is a $ref
			"shipments": map[string]interface{}{
				"address": "ship/new",
				"messages": map[string]interface{}{
					"shipment": map[string]interface{}{
						"correlationId": map[string]interface{}{"$ref": "#/components/correlationIds/byTrackingId"},
					},
				},
			},
			// two message kinds, each keeping its id somewhere different
			"mixed": map[string]interface{}{
				"address": "notify/mixed",
				"messages": map[string]interface{}{
					"alpha": map[string]interface{}{
						"correlationId": map[string]interface{}{"location": "$message.header#/traceId"},
					},
					"beta": map[string]interface{}{
						"correlationId": map[string]interface{}{"location": "$message.payload#/meta/id"},
					},
				},
			},
			// nothing declared anywhere — the fallback case
			"legacy": map[string]interface{}{
				"address":  "legacy/events",
				"messages": map[string]interface{}{"event": map[string]interface{}{"contentType": "application/json"}},
			},
		},
		"components": map[string]interface{}{
			"messages": map[string]interface{}{
				"audit": map[string]interface{}{
					"correlationId": map[string]interface{}{"location": "$message.payload#/auditId"},
				},
			},
			"correlationIds": map[string]interface{}{
				"byTrackingId": map[string]interface{}{"location": "$message.header#/trackingId"},
			},
		},
	}
	return map[string]interface{}{"bus": spec}
}

func TestDeclaredCorrelationLocations(t *testing.T) {
	af := NewAsyncFinder(correlationSources())

	cases := []struct {
		channel string
		want    []string
		why     string
	}{
		{"orders", []string{"$message.header#/correlationId"}, "declared inline on the message"},
		{"audits", []string{"$message.payload#/auditId"}, "the message is a $ref into components"},
		{"shipments", []string{"$message.header#/trackingId"}, "the Correlation ID Object itself is a $ref"},
		{"mixed", []string{"$message.header#/traceId", "$message.payload#/meta/id"}, "one per message kind, sorted by message key"},
		{"legacy", nil, "nothing declared -> empty, so the caller falls back"},
	}

	for _, c := range cases {
		info := af.FindChannelByPath("bus#/channels/" + c.channel)
		if info == nil {
			t.Fatalf("%s: channel did not resolve", c.channel)
		}
		got := info.DeclaredCorrelationLocations()
		if len(got) != len(c.want) {
			t.Errorf("%s (%s): got %v, want %v", c.channel, c.why, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s (%s): got %v, want %v", c.channel, c.why, got, c.want)
				break
			}
		}
	}

	// A nil AsyncInfo must not panic — resolveCorrelation is reached on any receive.
	var nilInfo *AsyncInfo
	if got := nilInfo.DeclaredCorrelationLocations(); got != nil {
		t.Errorf("nil AsyncInfo should yield no locations, got %v", got)
	}
}

func TestParseCorrelationLocation(t *testing.T) {
	cases := []struct {
		location string
		part     string
		pointer  string
		ok       bool
	}{
		{"$message.header#/correlationId", "header", "/correlationId", true},
		{"$message.payload#/user/id", "payload", "/user/id", true},
		{"  $message.header#/x  ", "header", "/x", true},
		{"$message.payload#", "payload", "", true},
		// Not a supported location: no '#', an unknown root, or a bare expression. Each must be a
		// clean miss rather than a match against some other part of the message.
		{"$message.header/correlationId", "", "", false},
		{"$message.metadata#/x", "", "", false},
		{"$request.header#/x", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		part, pointer, ok := parseCorrelationLocation(c.location)
		if ok != c.ok || part != c.part || pointer != c.pointer {
			t.Errorf("%q: got (%q,%q,%v), want (%q,%q,%v)", c.location, part, pointer, ok, c.part, c.pointer, c.ok)
		}
	}
}

// TestCorrelationLocationIgnoresDecoy is the reason this feature exists. Two messages are queued: a
// DECOY that carries the id in an unrelated field, and the real one carrying it where the AsyncAPI
// document says it lives. With a declared location only the real one may match.
func TestCorrelationLocationIgnoresDecoy(t *testing.T) {
	decoy := &Message{
		Headers: map[string]interface{}{"correlationId": "ORD-1"},
		Payload: map[string]interface{}{"orderId": "99", "customerId": "42"},
	}
	real := &Message{
		Headers: map[string]interface{}{"correlationId": "42"},
		Payload: map[string]interface{}{"orderId": "42"},
	}

	b := newMessageBuffer()
	b.push("ch", decoy)
	b.push("ch", real)

	corr := Correlation{ID: "42", Locations: []string{"$message.header#/correlationId"}}
	got, err := b.receive("ch", corr, time.Second)
	if err != nil {
		t.Fatalf("expected the correlated message, got %v", err)
	}
	if got != real {
		t.Fatalf("the decoy was matched: a declared location must be read exclusively, got payload %v", got.Payload)
	}

	// And the decoy is still queued — skipping must not consume it.
	if left, err := b.receive("ch", Correlation{}, time.Second); err != nil || left != decoy {
		t.Errorf("the skipped decoy should remain queued, got %v / %v", left, err)
	}
}

// Without a declared location the whole-message scan applies, and it produces false positives — in two
// DIFFERENT ways depending on whether the message arrived decoded or as bytes. This documents exactly
// what a declared location buys, and guards the fallback from being dropped.
func TestWithoutLocationTheScanProducesFalsePositives(t *testing.T) {
	// Decoded payload (the in-memory adapter): the scan compares every scalar for EQUALITY, so an
	// unrelated field that happens to hold the same value matches.
	wrongField := &Message{Payload: map[string]interface{}{"orderId": "99", "customerId": "42"}}
	b := newMessageBuffer()
	b.push("ch", wrongField)
	if got, err := b.receive("ch", Correlation{ID: "42"}, time.Second); err != nil || got != wrongField {
		t.Errorf("the decoded scan should match any scalar equal to the id, got %v / %v", got, err)
	}

	// Bytes only (every real-broker message): the scan is a SUBSTRING search, so the id matches even
	// buried inside prose — a strictly wider net than the decoded case.
	inProse := &Message{Raw: []byte(`{"orderId":"99","note":"see ticket 42"}`)}
	b2 := newMessageBuffer()
	b2.push("ch", inProse)
	if got, err := b2.receive("ch", Correlation{ID: "42"}, time.Second); err != nil || got != inProse {
		t.Errorf("the raw scan should match the id as a substring, got %v / %v", got, err)
	}

	// A declared location rules BOTH out.
	for name, m := range map[string]*Message{"wrong field": wrongField, "in prose": inProse} {
		b3 := newMessageBuffer()
		b3.push("ch", m)
		corr := Correlation{ID: "42", Locations: []string{"$message.header#/correlationId"}}
		if _, err := b3.receive("ch", corr, 60*time.Millisecond); err != ErrReceiveTimeout {
			t.Errorf("%s: a declared location must reject it, got %v", name, err)
		}
	}
}

// A declared location that the message does not carry is a MISS, never a fall-through to scanning.
func TestDeclaredLocationDoesNotFallBack(t *testing.T) {
	// The id is present, but in the payload — while the document says it lives in a header.
	m := &Message{Payload: map[string]interface{}{"orderId": "42"}}
	b := newMessageBuffer()
	b.push("ch", m)

	corr := Correlation{ID: "42", Locations: []string{"$message.header#/correlationId"}}
	if _, err := b.receive("ch", corr, 60*time.Millisecond); err != ErrReceiveTimeout {
		t.Fatalf("a declared location must not fall back to a whole-message scan, got %v", err)
	}
}

// A payload location on a bytes-only message (every real-broker message) needs the decoder the
// executor supplies; with it, the pointer resolves into the decoded body.
func TestCorrelationPayloadLocationDecodesRawBytes(t *testing.T) {
	raw := []byte(`{"meta":{"id":"TRK-7"},"weight":3}`)
	b := newMessageBuffer()
	b.push("ch", &Message{Raw: raw}) // no Payload — exactly what MQTT/WS deliver

	corr := Correlation{
		ID:        "TRK-7",
		Locations: []string{"$message.payload#/meta/id"},
		Decode:    func(r []byte) (interface{}, error) { var v interface{}; err := json.Unmarshal(r, &v); return v, err },
	}
	if _, err := b.receive("ch", corr, time.Second); err != nil {
		t.Fatalf("a payload location should resolve through the decoder, got %v", err)
	}

	// Without a decoder the payload cannot be read at all — a miss, not a scan of the raw bytes
	// (which would match, since "TRK-7" appears in them).
	b2 := newMessageBuffer()
	b2.push("ch", &Message{Raw: raw})
	noDecoder := Correlation{ID: "TRK-7", Locations: []string{"$message.payload#/meta/id"}}
	if _, err := b2.receive("ch", noDecoder, 60*time.Millisecond); err != ErrReceiveTimeout {
		t.Fatalf("without a decoder a payload location must miss rather than scan raw bytes, got %v", err)
	}
}

// A channel whose message kinds keep their ids in different places must match either kind.
func TestCorrelationChecksEveryDeclaredLocation(t *testing.T) {
	corr := Correlation{
		ID:        "X-9",
		Locations: []string{"$message.header#/traceId", "$message.payload#/meta/id"},
	}

	inHeader := &Message{Headers: map[string]interface{}{"traceId": "X-9"}}
	inPayload := &Message{Payload: map[string]interface{}{"meta": map[string]interface{}{"id": "X-9"}}}

	for name, m := range map[string]*Message{"header kind": inHeader, "payload kind": inPayload} {
		b := newMessageBuffer()
		b.push("ch", m)
		if _, err := b.receive("ch", corr, time.Second); err != nil {
			t.Errorf("%s should match one of the declared locations, got %v", name, err)
		}
	}
}
