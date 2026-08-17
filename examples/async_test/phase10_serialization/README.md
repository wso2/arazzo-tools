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
| `text/plain` | text | raw UTF-8 string; scalars (numbers, booleans) stringified, objects/arrays **fail** |
| `application/x-protobuf`, `application/protobuf` | protobuf **stub** | selects but fails "not supported yet" — Phase 11 |
| `application/avro`, `avro/binary` | avro **stub** | selects but fails "not supported yet" — Phase 11 |
| anything else | — | **hard error** naming what actually works, and separately what is only recognized (never guesses) |

## Scenarios (every Phase 10 behavior)

| file | what it shows | workflow(s) | expected |
|---|---|---|---|
| `01-text-plain.arazzo.yaml` | `contentType: text/plain` → payload is raw text, not JSON | `textFlow` | ✅ `heard = "system reboot at 02:00"` |
| `02-json-default.arazzo.yaml` | nothing declares a content type **anywhere** → the JSON last resort | `jsonFlow` | ✅ `note = "v2 shipped"` |
| `03-unsupported-contenttype.arazzo.yaml` | unknown content type → fails loudly at send, listing what works and what is only recognized | `badFlow` | ❌ "no serializer registered ... (supported: application/json, text/plain; recognized but not yet implemented: …)" |
| `04-protobuf-stub.arazzo.yaml` | Protobuf recognized but stubbed → fails clearly instead of mis-encoding | `protoFlow` | ❌ "protobuf serialization (application/x-protobuf) is not supported yet" |
| `05-avro-stub.arazzo.yaml` | Avro recognized but stubbed → fails clearly instead of mis-encoding | `avroFlow` | ❌ "avro serialization (application/avro) is not supported yet" |
| `06-contenttype-normalization.arazzo.yaml` | `+json` suffix and `; charset=…` params both normalize to JSON | `suffixFlow`, `paramsFlow` | ✅ `id = "S-1"` / `id = "P-1"` |
| `07-declared-contenttype.arazzo.yaml` | step omits `contentType` → the **AsyncAPI document** decides; two channels, two formats | `declaredFlow` | ✅ `heard = "all clear"` (bare text), `kind = "deploy"` |
| `08-ref-and-operationpath.arazzo.yaml` | contentType declared behind a **`$ref`** into `components.messages`, reached by **`operationPath`** | `auditFlow` | ✅ `entry = "entry 1"` (bare text) |
| `09-contenttype-mismatch.arazzo.yaml` | step says `application/json`, channel says `text/plain` → **the step overrides the declaration, both layers warn** | `mismatchFlow` | ✅ `heard = "beta"` + a warning in the log and a yellow squiggle in the editor |
| `10-default-contenttype.arazzo.yaml` | document-level **`defaultContentType`**, and a message overriding it | `defaultsFlow` | ✅ `reading = "23.5"`, `kind = "threshold"` |
| `11-structured-payload-as-text.arazzo.yaml` | object payload on a `text/plain` channel → **hard error** | `badTextFlow` | ❌ "cannot serialize a structured payload (object/array) as text/plain" |
| `12-targeting-forms.arazzo.yaml` | `channelPath` / `operationId` / `operationPath` all reach the same `$ref`'d declaration | `formsFlow` | ✅ `viaChannel = "A"`, `viaOperationId = "B"`, `viaOperationPath = "C"` |
| `13-ambiguous-channel.arazzo.yaml` | channel declares **two different** message formats → the runtime guesses and says so | `ambiguousFlow` | ✅ `guessed = "hello"`, `chosen = "hello"` + one warning naming the guessing step |

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
test_runner examples/async_test/phase10_serialization/13-ambiguous-channel.arazzo.yaml ambiguousFlow
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
| `09-contenttype-mismatch.arazzo.yaml` | `emitJson` | **yellow/warning**: the step's contentType disagrees with the document's; the step's value overrides the AsyncAPI declaration |
| `13-ambiguous-channel.arazzo.yaml` | `sendGuessed` | **yellow/warning**: the channel declares more than one contentType; which one will be used, and to set it on the step |
| `13-ambiguous-channel.arazzo.yaml` | `sendChosen` | **nothing** — the step declared its own contentType, so nothing is ambiguous |
| `13-ambiguous-channel.arazzo.yaml` | `takeGuessed`, `takeChosen` | **yellow/warning**: a receive picks a decoder too, and has no requestBody to settle it — so the advice points at the document |
| `12-targeting-forms.arazzo.yaml` | every step | **nothing** — the `$ref`'d declaration resolves through all three targeting forms, so there is nothing to report |

If 09 shows no warning, the source index has not been built for the document — that is the bug fixed
by having the diagnostics resolvers index on demand rather than racing the background indexing pass.

## Seeing which serializer actually ran

A workflow's **outputs cannot show you this**. The in-memory adapter (Phase 9) hands the receive step
the decoded payload it was given, alongside the bytes, so the receive never decodes and the output is
identical whichever serializer ran. Only a real broker (Phase 11) transmits bytes alone.

**The run log shows it** (the pseudo-terminal the extension opens for the server, or the CLI's own
output). Every send logs the encoder it resolved and the exact bytes it put on the wire, and every
receive logs the decoder that governed the message:

```text
Step raiseAlert:  ... as text/plain (9 bytes): "all clear"
Step publishEvent: ... as application/json (37 bytes): "{\"kind\":\"deploy\",\"note\":\"v3 shipped\"}"
```

The bytes are quoted, so the difference is visible directly. A string sent as **text** carries no
quotes; the same string sent as **JSON** carries them — that is what the escaped `\"` are:

```text
09 (string as JSON):  as application/json (6 bytes): "\"beta\""
01 (string as text): as text/plain (22 bytes): "system reboot at 02:00"
```

So `beta` is 4 bytes as text and 6 as JSON — the two extra bytes are the quote characters a consumer
reading plain text would not expect.

Receives report the other half:

```text
Step takeReading: ... via in-memory adapter, decoded as text/plain
Step takeEvent:   ... via in-memory adapter, decoded as application/json
```

**In the editor**, the same facts appear on the step's **Logs** section: expand the SEND/RECEIVE entry
and the Channel block lists **Encoder** (on a send) or **Decoder** (on a receive), next to Adapter and
Correlation ID.

Unit tests assert the same bytes automatically: `TestSendFallsBackToAsyncAPIDeclaredContentType`,
`TestSendFallsBackToDocumentDefaultContentType`, `TestSendStepContentTypeWinsOverDeclared`,
`TestDeclaredContentTypeFollowsMessageRef` and `TestAsyncSendUsesContentTypeSerializer` pull the raw
bytes back off the adapter and check them, and `TestReceiveUsesDeclaredContentTypeForRawBytes` seeds a
bytes-only message to exercise the real-broker decode path.

## What Phase 10 delivers

- A `Serializer` interface (`Serialize`/`Deserialize` + `ContentType`/`Name`) and a
  `SerializerRegistry` that selects one by content type (parameters like `; charset=utf-8` stripped,
  case-insensitive, `+json` structured suffix → JSON).
- JSON (default) and text/plain fully implemented; Protobuf/Avro registered as stubs that fail with a
  plain "not supported yet" (completed with real brokers in Phase 11). A structured payload on a
  text/plain channel is an error, not the runtime's own rendering of a map.
- The runner **serializes on send** and **deserializes on receive** (when the adapter delivers only
  bytes) through the precedence table above — not from `requestBody.contentType` alone. The in-memory
  adapter still carries the decoded payload, so JSON workflows behave exactly as before.
- Both directions report the serializer they used, and warn when they had to guess it; the editor
  flags the same cases while authoring.
