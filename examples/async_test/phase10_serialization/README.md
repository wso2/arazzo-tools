# Phase 10 — Message Serialization Layer examples

Phase 10 separates a message's **shape** (the `payload`/headers the runtime reasons about) from its
**wire format** (the bytes a channel carries). A step builds a logical payload; a **serializer**
encodes it to bytes on send and decodes it back into `$message.payload` on receive. Adapters
(Phase 9/11) move bytes only — they never encode.

**Which serializer is chosen** follows Arazzo §5.8.14.1 — *"The Content-Type for the request content.
If omitted then refer to Content-Type specified at the targeted operation to understand serialization
requirements."* So JSON is the last resort, not the first answer:

| | send | receive |
|---|---|---|
| 1 | the step's `requestBody.contentType` | the content type the transport carried, if any |
| 2 | the AsyncAPI message's `contentType` (inline or behind a `$ref`) | *(same)* |
| 3 | the AsyncAPI document's `defaultContentType` | *(same)* |
| 4 | `application/json` | *(same)* |

A receive step has no `requestBody`, so it has no step-level content type; and MQTT 3.1.1/WebSocket
carry no format label at all, which is why the AsyncAPI document is what tells the runtime how to read
the bytes a real broker delivers. Steps 2–3 follow AsyncAPI's own precedence (*"When omitted, the value
MUST be the one specified on the defaultContentType field"*).

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
| `02-json-default.arazzo.yaml` | nothing declares a content type **anywhere** → the JSON last resort | `jsonFlow` | ✅ `note = "v2 shipped"` |
| `03-unsupported-contenttype.arazzo.yaml` | unknown content type → fails loudly at send | `badFlow` | ❌ "no serializer registered for content type ..." |
| `04-protobuf-stub.arazzo.yaml` | Protobuf recognized but stubbed → clear "needs `.proto`/descriptor" | `protoFlow` | ❌ "protobuf serialization ... is not yet implemented" |
| `05-avro-stub.arazzo.yaml` | Avro recognized but stubbed → clear "needs schema/registry" | `avroFlow` | ❌ "avro serialization ... is not yet implemented" |
| `06-contenttype-normalization.arazzo.yaml` | `+json` suffix and `; charset=…` params both normalize to JSON | `suffixFlow`, `paramsFlow` | ✅ `id = "S-1"` / `id = "P-1"` |
| `07-declared-contenttype.arazzo.yaml` | step omits `contentType` → the **AsyncAPI document** decides; two channels, two formats | `declaredFlow` | ✅ `heard = "all clear"` (bare text), `kind = "deploy"` |
| `08-ref-and-operationpath.arazzo.yaml` | contentType declared behind a **`$ref`** into `components.messages`, reached by **`operationPath`** | `auditFlow` | ✅ `entry = "entry 1"` (bare text) |
| `09-contenttype-mismatch.arazzo.yaml` | step says `application/json`, channel says `text/plain` → **step wins, both layers warn** | `mismatchFlow` | ✅ `heard = "beta"` + a warning in the log and a yellow squiggle in the editor |
| `10-default-contenttype.arazzo.yaml` | document-level **`defaultContentType`**, and a message overriding it | `defaultsFlow` | ✅ `reading = "23.5"`, `kind = "threshold"` |
| `11-structured-payload-as-text.arazzo.yaml` | object payload on a `text/plain` channel → **hard error** | `badTextFlow` | ❌ "cannot serialize a structured payload (object/array) as text/plain" |
| `12-targeting-forms.arazzo.yaml` | `channelPath` / `operationId` / `operationPath` all reach the same `$ref`'d declaration | `formsFlow` | ✅ `viaChannel = "A"`, `viaOperationId = "B"`, `viaOperationPath = "C"` |

The one serializer behavior **not** expressible as an example is the receive-side **deserialize of
raw bytes** (the path a real broker takes when it delivers bytes + a content type, with no pre-decoded
object). A pure Arazzo doc can't reach it because the in-memory adapter always carries the decoded
`Payload` on send — so it's covered by the unit test `TestAsyncReceiveDeserializesRawBytes`
(and `serializer_test.go`) instead — together with
`TestReceiveUsesDeclaredContentTypeForRawBytes`, which covers the same path when the bytes arrive with
no content type at all and the AsyncAPI declaration is the only thing that says how to decode them.

## How to run / verify

Via the CLI test runner (`test_runner <file> <workflowId> [input-json]`):

```sh
test_runner examples/async_test/phase10_serialization/01-text-plain.arazzo.yaml textFlow
test_runner examples/async_test/phase10_serialization/02-json-default.arazzo.yaml jsonFlow
test_runner examples/async_test/phase10_serialization/03-unsupported-contenttype.arazzo.yaml badFlow      # fails on purpose
test_runner examples/async_test/phase10_serialization/04-protobuf-stub.arazzo.yaml protoFlow              # fails on purpose
test_runner examples/async_test/phase10_serialization/05-avro-stub.arazzo.yaml avroFlow                   # fails on purpose
test_runner examples/async_test/phase10_serialization/06-contenttype-normalization.arazzo.yaml suffixFlow
test_runner examples/async_test/phase10_serialization/06-contenttype-normalization.arazzo.yaml paramsFlow
test_runner examples/async_test/phase10_serialization/07-declared-contenttype.arazzo.yaml declaredFlow
test_runner examples/async_test/phase10_serialization/08-ref-and-operationpath.arazzo.yaml auditFlow
test_runner examples/async_test/phase10_serialization/09-contenttype-mismatch.arazzo.yaml mismatchFlow
test_runner examples/async_test/phase10_serialization/10-default-contenttype.arazzo.yaml defaultsFlow
test_runner examples/async_test/phase10_serialization/11-structured-payload-as-text.arazzo.yaml badTextFlow  # fails on purpose
test_runner examples/async_test/phase10_serialization/12-targeting-forms.arazzo.yaml formsFlow
```

The four ❌ workflows (`badFlow`, `protoFlow`, `avroFlow`, `badTextFlow`) are meant to end in error —
that error **is** the expected outcome (no serializer, a stubbed one, or a payload the declared format
cannot carry).

## What to check in the editor

Three of these show up as diagnostics while authoring, not only at run time. Open the file in the
extension and look at the step:

| file | step | what you should see |
|---|---|---|
| `02-json-default.arazzo.yaml` | `emit` | **blue/information**: no contentType on the step and none in the document — the message will be serialized as `application/json` |
| `09-contenttype-mismatch.arazzo.yaml` | `emitJson` | **yellow/warning**: the step's contentType disagrees with the document's; the step's value wins |
| `12-targeting-forms.arazzo.yaml` | every step | **nothing** — the `$ref`'d declaration resolves through all three targeting forms, so there is nothing to report |

If 09 shows no warning, the source index has not been built for the document — that is the bug fixed
by having the diagnostics resolvers index on demand rather than racing the background indexing pass.

## Why an example cannot show you the raw bytes

Phase 10 decides **which serializer encodes a message**, and the visible proof of that is the bytes on
the wire. An Arazzo example cannot show them: the in-memory adapter (Phase 9) hands the receive step
the decoded payload it was given, alongside the bytes, so the receive never has to decode and the
output is the same whichever serializer ran. Only a real broker (Phase 11) transmits bytes alone.

So these examples demonstrate **which serializer gets chosen** — and fail loudly when that choice is
impossible (03, 04, 05, 11) — while the byte-level assertions live in unit tests:
`TestSendFallsBackToAsyncAPIDeclaredContentType`, `TestSendFallsBackToDocumentDefaultContentType`,
`TestSendStepContentTypeWinsOverDeclared`, `TestDeclaredContentTypeFollowsMessageRef` and
`TestAsyncSendUsesContentTypeSerializer` all pull the raw bytes back off the adapter and check them,
and `TestReceiveUsesDeclaredContentTypeForRawBytes` seeds a bytes-only message to exercise the
real-broker decode path.

## What Phase 10 delivers

- A `Serializer` interface (`Serialize`/`Deserialize` + `ContentType`/`Name`) and a
  `SerializerRegistry` that selects one by content type (parameters like `; charset=utf-8` stripped,
  case-insensitive, `+json` structured suffix → JSON).
- JSON (default) and text/plain fully implemented; Protobuf/Avro registered as clear
  "needs schema config" stubs (completed with real brokers in Phase 11).
- The runner **serializes on send** (from `requestBody.contentType`) and **deserializes on receive**
  when the adapter delivers only bytes — the in-memory adapter still carries the decoded payload, so
  JSON workflows behave exactly as before.
