# Phase 10 — Message Serialization Layer examples

Phase 10 separates a message's **shape** (the `payload`/headers the runtime reasons about) from its
**wire format** (the bytes a channel carries). A step builds a logical payload; a **serializer**,
chosen by the requestBody's `contentType`, encodes it to bytes on send and decodes it back into
`$message.payload` on receive. Adapters (Phase 9/11) move bytes only — they never encode.

Supported serializers (see [serializer.go](../../../arazzo-designer-cli/internal/runner/executor/serializer.go)):

| content type | serializer | notes |
|---|---|---|
| _(none)_ / `application/json` / `*+json` | JSON | default; object ⇄ JSON bytes |
| `text/plain` | text | raw UTF-8 string (non-strings stringified) |
| `application/x-protobuf`, `application/protobuf` | protobuf **stub** | selects but fails "needs `.proto`/descriptor" — Phase 11 |
| `application/avro`, `avro/binary` | avro **stub** | selects but fails "needs Avro schema/registry" — Phase 11 |
| anything else | — | **hard error** listing supported types (never guesses) |

## Scenarios (every Phase 10 behavior)

| file | what it shows | workflow(s) | expected |
|---|---|---|---|
| `01-text-plain.arazzo.yaml` | `contentType: text/plain` → payload is raw text, not JSON | `textFlow` | ✅ `heard = "system reboot at 02:00"` |
| `02-json-default.arazzo.yaml` | no `contentType` → JSON object round-trips | `jsonFlow` | ✅ `note = "v2 shipped"` |
| `03-unsupported-contenttype.arazzo.yaml` | unknown content type → fails loudly at send | `badFlow` | ❌ "no serializer registered for content type ..." |
| `04-protobuf-stub.arazzo.yaml` | Protobuf recognized but stubbed → clear "needs `.proto`/descriptor" | `protoFlow` | ❌ "protobuf serialization ... is not yet implemented" |
| `05-avro-stub.arazzo.yaml` | Avro recognized but stubbed → clear "needs schema/registry" | `avroFlow` | ❌ "avro serialization ... is not yet implemented" |
| `06-contenttype-normalization.arazzo.yaml` | `+json` suffix and `; charset=…` params both normalize to JSON | `suffixFlow`, `paramsFlow` | ✅ `id = "S-1"` / `id = "P-1"` |

The one serializer behavior **not** expressible as an example is the receive-side **deserialize of
raw bytes** (the path a real broker takes when it delivers bytes + a content type, with no pre-decoded
object). A pure Arazzo doc can't reach it because the in-memory adapter always carries the decoded
`Payload` on send — so it's covered by the unit test `TestAsyncReceiveDeserializesRawBytes`
(and `serializer_test.go`) instead.

## How to run / verify

Via the CLI test runner (`test_runner <file> <workflowId> [input-json]`):

```
test_runner examples/async_test/phase10/01-text-plain.arazzo.yaml textFlow
test_runner examples/async_test/phase10/02-json-default.arazzo.yaml jsonFlow
test_runner examples/async_test/phase10/03-unsupported-contenttype.arazzo.yaml badFlow      # fails on purpose
test_runner examples/async_test/phase10/04-protobuf-stub.arazzo.yaml protoFlow              # fails on purpose
test_runner examples/async_test/phase10/05-avro-stub.arazzo.yaml avroFlow                   # fails on purpose
test_runner examples/async_test/phase10/06-contenttype-normalization.arazzo.yaml suffixFlow
test_runner examples/async_test/phase10/06-contenttype-normalization.arazzo.yaml paramsFlow
```

The three ❌ workflows (`badFlow`, `protoFlow`, `avroFlow`) are meant to end in error — that error
**is** the expected outcome (no/stubbed serializer for the content type).

## What Phase 10 delivers

- A `Serializer` interface (`Serialize`/`Deserialize` + `ContentType`/`Name`) and a
  `SerializerRegistry` that selects one by content type (parameters like `; charset=utf-8` stripped,
  case-insensitive, `+json` structured suffix → JSON).
- JSON (default) and text/plain fully implemented; Protobuf/Avro registered as clear
  "needs schema config" stubs (completed with real brokers in Phase 11).
- The runner **serializes on send** (from `requestBody.contentType`) and **deserializes on receive**
  when the adapter delivers only bytes — the in-memory adapter still carries the decoded payload, so
  JSON workflows behave exactly as before.
