# Phase 9 — AsyncAPI Adapter Runtime examples

Phase 9 makes AsyncAPI steps **actually execute**: `action: send` publishes a message and
`action: receive` waits for one — through a broker-agnostic **adapter**. Phase 9 ships the built-in
**in-memory adapter** (no external broker needed); real brokers (Kafka/MQTT/WebSocket) are Phase 11.

| File | Scenario | Workflow | Expected |
|---|---|---|---|
| `order-events.asyncapi.yaml` | AsyncAPI source — `orders` channel + `placeOrder`(send)/`consumeOrder`(receive) ops | — | — |
| `01-send-receive.arazzo.yaml` | send → receive round trip; `$message` criteria + outputs | `roundTrip` | ✅ completes, `orderId="ORD-1"` |
| `02-correlation.arazzo.yaml` | two messages sent; **correlated** receive picks the right one | `correlate` | ✅ completes, `matched="ORD-B"` |
| `03-operationid-async.arazzo.yaml` | async via **operationId** (direction from the operation) | `byOperationId` | ✅ completes, `orderId="OP-1"` |
| `04-receive-timeout.arazzo.yaml` | receive with nothing sent → **timeout** | `timeoutFlow` | ❌ fails: *receive timed out* |
| `05-channelpath-no-action.arazzo.yaml` | `channelPath` with **no `action`** → hard error | `badFlow` | ❌ fails: *requires 'action'* |
| `06-criteria-failure.arazzo.yaml` | received message fails **successCriteria** | `mismatchFlow` | ❌ fails: *did not satisfy successCriteria* |
| `07-action-mismatch.arazzo.yaml` | step `action` contradicts operation → **operation wins** (warns) | `mismatchWins` | ✅ completes (runs as send), `orderId="ORD-M"` |
| `08-send-outputs-correlation.arazzo.yaml` | a **send** step's `outputs` + `successCriteria` work; the reply correlates on what the send recorded | `sendOutputs` | ✅ completes, `echoed="ORD-B"`, `sent="ORD-B"` |
| `09-send-criteria-failure.arazzo.yaml` | a **send** step's `successCriteria` can **fail** it | `badSend` | ❌ fails: *sent message did not satisfy successCriteria* |

## How to run / verify

Via the CLI test runner (`test_runner <file> <workflowId> [input-json]`):

```
test_runner examples/async_test/phase9/01-send-receive.arazzo.yaml roundTrip
test_runner examples/async_test/phase9/02-correlation.arazzo.yaml correlate '{"wantOrder":"ORD-B"}'
test_runner examples/async_test/phase9/03-operationid-async.arazzo.yaml byOperationId
test_runner examples/async_test/phase9/04-receive-timeout.arazzo.yaml timeoutFlow          # fails on purpose
test_runner examples/async_test/phase9/05-channelpath-no-action.arazzo.yaml badFlow        # fails on purpose
test_runner examples/async_test/phase9/06-criteria-failure.arazzo.yaml mismatchFlow        # fails on purpose
test_runner examples/async_test/phase9/07-action-mismatch.arazzo.yaml mismatchWins
test_runner examples/async_test/phase9/08-send-outputs-correlation.arazzo.yaml sendOutputs '{"orderId":"ORD-B"}'
test_runner examples/async_test/phase9/09-send-criteria-failure.arazzo.yaml badSend         # fails on purpose
```

The four ❌ workflows are meant to end in error — that error **is** the expected outcome (timeout /
missing-action / criteria enforcement doing their job). Scenario 07 logs a
`contradicts the AsyncAPI operation's action` warning and then runs as the operation's action.

Scenario 08 is the one to look at for **why a send step's outputs matter**: two orders sit on the same
channel and the receive correlates on `$steps.emitMine.outputs.sentId`. It returns `ORD-B` — our order.
If the send step's outputs were dropped, that expression would resolve to empty, the receive would fall
back to plain FIFO, and it would silently return `ORD-A` instead.

## What Phase 9 delivers
- A broker-agnostic **`Adapter`** interface (`Send` / `Receive`) and a built-in **`InMemoryAdapter`**.
- The runner **routes async steps** (`channelPath`, or an `operationId` that resolves to an AsyncAPI
  operation) to the adapter instead of the HTTP path.
- **`correlationId`** matching, **`timeout`**, and the **`$message`** runtime expression.
- Runtime enforcement: a `channelPath` step without `action` is a hard error; a step `action` that
  contradicts the AsyncAPI operation's action warns and defers to the operation.

## Notes / current limitations
- **In-memory only.** Messages live in-process for the run; there is no persistence and no real
  broker. Real broker adapters are Phase 11; the serialization layer (Avro/Protobuf/…) is Phase 10.
- **Correlation** in the in-memory adapter is a simple heuristic (matches a value in the message
  headers/payload; empty ⇒ FIFO). A self-referential `correlationId: $message.*` resolves to nil at
  receive time (the message doesn't exist yet), so it falls back to FIFO — which is why the round-trip
  example needs no explicit correlation. Real brokers correlate on explicit message headers.
- **successCriteria / outputs** work on **both** async directions and reuse the exact same
  checker/extractor as HTTP steps — they just read `$message` instead of `$response`. Inside a
  **receive** step `$message` is the message that arrived; inside a **send** step it is the message
  that step published (same `{header, payload}` shape). A send step is not special: declaring either
  field on it behaves exactly as it does on any other step.
