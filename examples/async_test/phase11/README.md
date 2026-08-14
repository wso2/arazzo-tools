# Phase 11 — Real Broker Adapters (WebSocket + MQTT) examples

Phase 11 gives the runner **real network transports**. The AsyncAPI document's `servers` section
decides which adapter a step runs on:

| `servers.<name>.protocol` | adapter | connects to |
|---|---|---|
| `ws` / `wss` | WebSocket ([adapter_ws.go](../../../arazzo-designer-cli/internal/runner/executor/adapter_ws.go)) | `ws(s)://<host>/<channel address>` |
| `mqtt` / `mqtts` | MQTT ([adapter_mqtt.go](../../../arazzo-designer-cli/internal/runner/executor/adapter_mqtt.go)) | `tcp://<host>:1883` / `ssl://<host>:8883` (channel address = topic) |
| *(no `servers` section)* | in-memory (Phase 9) | nothing — in-process queues, for tests/local runs |
| `kafka` | — | **clear "not yet supported" error** (future phase, with Avro/Protobuf) |

Selection lives in [adapter_select.go](../../../arazzo-designer-cli/internal/runner/executor/adapter_select.go);
adapters are cached per `protocol://host` so all steps against one broker share a connection. On
receive, bytes are decoded using the channel's declared message `contentType` (transports don't
carry one), defaulting to JSON — the Phase 10 serializer layer doing its real job.

## Scenarios (every MQTT + WebSocket + selection case)

| file | what it shows | workflow | expected |
|---|---|---|---|
| `01-mqtt-roundtrip.arazzo.yaml` | MQTT publish + subscribe (channelPath) on the **real public HiveMQ broker** | `mqttRoundTrip` | ✅ `sentBack = 21.5` |
| `02-ws-echo.arazzo.yaml` | WebSocket send + receive (channelPath) via the **real public echo server** (TLS/`wss`) | `wsEcho` | ✅ `echoed = "hello over websocket"` |
| `03-kafka-unsupported.arazzo.yaml` | `kafka` protocol → clear planned-future error | `kafkaFlow` | ❌ "the \"kafka\" protocol is not yet supported ..." |
| `04-mqtt-operationid.arazzo.yaml` | MQTT round trip via **operationId** (direction from the AsyncAPI operation) | `byOperation` | ✅ `got = 19` |
| `05-mqtt-timeout.arazzo.yaml` | MQTT receive on a quiet topic → **times out** | `mqttTimeout` | ❌ "timed out after 3s: no message arrived" |
| `06-mqtt-text-plain.arazzo.yaml` | **text/plain** wire format over MQTT (Phase-10 serializer over a real transport) | `textAlert` | ✅ `heard = "ALERT token=txt-8 ..."` |
| `07-mqtts-tls.arazzo.yaml` | MQTT over **TLS** (`mqtts` → `ssl://…:8883`) | `mqttsRoundTrip` | ✅ `sentBack = 23.7` |
| `08-ws-timeout.arazzo.yaml` | WebSocket receive with no matching frame → **times out** | `wsTimeout` | ❌ "timed out ... no message matching correlationId ..." |
| `09-unknown-protocol.arazzo.yaml` | `amqp` protocol → clear unsupported error | `amqpFlow` | ❌ "unsupported AsyncAPI server protocol \"amqp\" ..." |
| `10-inmemory-fallback.arazzo.yaml` | **no `servers`** → in-memory adapter (no broker, no network) | `localFlow` | ✅ `echoedId = "L-1"` |

Coverage: MQTT (channelPath, operationId, text/plain, TLS, timeout), WebSocket (round trip, timeout),
and selection (mqtt, mqtts, ws/wss, kafka→error, unknown→error, no-servers→in-memory). The four ❌
workflows are meant to end in error — that error **is** the expected outcome.

## How to run / verify (01–09 need internet; 10 does not)

Via the CLI test runner (`test_runner <file> <workflowId> [input-json]`):

```
test_runner examples/async_test/phase11/01-mqtt-roundtrip.arazzo.yaml mqttRoundTrip '{"token":"demo-42"}'
test_runner examples/async_test/phase11/02-ws-echo.arazzo.yaml wsEcho '{"token":"demo-7"}'
test_runner examples/async_test/phase11/03-kafka-unsupported.arazzo.yaml kafkaFlow          # fails on purpose
test_runner examples/async_test/phase11/04-mqtt-operationid.arazzo.yaml byOperation '{"token":"op-33"}'
test_runner examples/async_test/phase11/05-mqtt-timeout.arazzo.yaml mqttTimeout             # fails on purpose
test_runner examples/async_test/phase11/06-mqtt-text-plain.arazzo.yaml textAlert '{"token":"txt-8"}'
test_runner examples/async_test/phase11/07-mqtts-tls.arazzo.yaml mqttsRoundTrip '{"token":"tls-5"}'
test_runner examples/async_test/phase11/08-ws-timeout.arazzo.yaml wsTimeout '{"token":"no-echo-7b21e"}'   # fails on purpose
test_runner examples/async_test/phase11/09-unknown-protocol.arazzo.yaml amqpFlow            # fails on purpose
test_runner examples/async_test/phase11/10-inmemory-fallback.arazzo.yaml localFlow
```

Notes:
- **`correlationId` must actually MATCH an arriving message, or the step times out.** A bare literal
  works as an id; only an *absent* `correlationId` makes the receive unfiltered (FIFO). The examples
  pass the token via `$inputs` so you can vary it — but run one with the wrong value and every message
  is skipped, and the timeout looks exactly like a dead channel rather than a filter miss.
- **The MQTT topic is public** (`arazzo/phase11/readings` on `broker.hivemq.com`) — anyone can
  publish there. The receive uses `correlationId: $inputs.token` (the sent payload carries the same
  token) so it matches OUR message and ignores strangers'. Pick a fresh token if a stale one lingers.
- **The WS echo server greets each connection** with one "Request served by …" line. The
  `correlationId` token skips that greeting and matches our echoed JSON frame.
- Both examples send first and receive second on the same channel. That works because the adapters
  **subscribe/listen before publishing** (MQTT subscribes to the topic before the publish; the WS
  reader starts at connect time), so the broker's copy of our own message is already being captured
  when the receive step runs.

## What Phase 11 delivers

- **`WSAdapter`** — one connection per channel (`scheme://host/<address>`), a reader goroutine
  buffering everything the server pushes, TLS via `wss`.
- **`MQTTAdapter`** — real `paho.mqtt.golang` client, QoS 1, subscribe-before-publish so same-workflow
  round trips work; the paho client sits behind a tiny interface so unit tests run against a fake.
- **Adapter selection** from AsyncAPI `servers.protocol` + `host`, cached per broker; no `servers` →
  in-memory (all Phase 9/10 examples run unchanged); unknown/kafka protocols → clear errors.
- **Shared `messageBuffer`** — the per-channel FIFO + correlation matching + timeout logic used by
  ALL adapters (extracted from the in-memory adapter; raw-byte correlation added for real brokers).

**Deferred (TODO in adapter_select.go):** Kafka adapter + real Avro/Protobuf codecs with
schema-registry config (they belong together — Kafka is where those formats are actually used).
Also: MQTT username/password + custom TLS config; correlation from schema-declared locations.
