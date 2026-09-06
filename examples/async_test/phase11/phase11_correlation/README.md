# Phase 11 (correlation) — Correlation ID Locations

AsyncAPI lets a message declare **where its correlation id lives**, as a runtime expression:

```yaml
messages:
  order:
    correlationId:
      location: $message.header#/correlationId
```

That declaration is the contract between publisher and subscriber. When it is present, a `receive`
step reads the id from exactly that place and nowhere else. When it is absent, the runtime has no
choice but to search the **entire message**, and that search can match a message which merely happens
to carry the same value somewhere unrelated — so the workflow continues on the wrong message and
reports success.

These three examples isolate that single variable. They all use the in-memory adapter, so they need
**no broker and no network**.

## The rule

| the AsyncAPI document… | what a receive does |
|---|---|
| declares `correlationId.location` | reads the id **only** there; a message not carrying it there does not match, full stop — there is no fall-through to searching |
| declares several (one per message kind) | checks **every** declared location; a match at any one is a match |
| declares none | searches the whole message: metadata, every header, every scalar in the payload, and — for a message that arrived as bytes, i.e. any real broker — the raw body as a **substring** |

The fallback fails in two different ways worth keeping straight:

- **decoded payload** (in-memory): scalar **equality**, so an unrelated field holding the same value matches;
- **raw bytes** (MQTT, WebSocket): **substring**, so an id of `42` matches a body reading `"see ticket 42"` — a strictly wider net.

## Scenarios

Every one of them plants a **decoy** — a message carrying the wanted value somewhere the document does
not name. Whether the decoy is matched is the whole subject.

| file | what it shows | run it with | expected |
|---|---|---|---|
| `01-declared-location` | the id is read from the declared header; a decoy carrying the value in `payload.customerId` is ignored | `{"want":"42"}` | ✅ `matched = "42"` |
| `02-no-location` | **the same workflow** on a channel declaring nothing — the decoy wins | `{"want":"42"}` | ⚠️ `matched = "99"` (the decoy) |
| `03-refd-location` | the location is two `$ref`s away and points into the **payload** | `{"want":"AUD-7"}` | ✅ `detail = "user login"` |
| `04-multiple-locations` | two message kinds keeping the id in different places — **both** locations are live | `{"a":"TRACE-1","b":"META-2"}` | ✅ `viaHeader = "alpha-order"`, `viaPayload = "beta-order"` |
| `05-published-to-wrong-place` | the publisher puts the id somewhere the document does not name | `{"want":"42"}` | ❌ times out — no fall-through |
| `06-unsupported-location` | the document names a location the runtime cannot read (`$message.metadata#/…`) | `{"want":"42"}` | ❌ times out — a clean miss, not a scan |
| `07-targeting-forms` | the same declared location reached by `channelPath`, `operationId` **and** `operationPath` | `{"a":"C-1","b":"C-2","c":"C-3"}` | ✅ `ch` / `op` / `path` |
| `08-payload-only-ignores-header` | a declared **payload** location means even a header named `correlationId` is not read | `{"want":"42"}` | ✅ `matched = "42"` |

Read **01 and 02 as a pair** — byte-for-byte the same workflow apart from the channel, returning
different orders. That difference *is* the feature.

Read **05 and 06 as the price of it**. Both publish a message a whole-message scan would have matched,
and both time out. A declared location that is not honoured — by the publisher (05) or because it
cannot be read (06) — matches nothing rather than quietly falling back, because falling back would
reintroduce exactly the imprecision the declaration removes. The failure looks like a dead channel even
though a message is sitting right there.

```bash
test_runner examples/async_test/phase11/phase11_correlation/01-declared-location.arazzo.yaml preciseMatch '{"want":"42"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/02-no-location.arazzo.yaml impreciseMatch '{"want":"42"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/03-refd-location.arazzo.yaml auditTrail '{"want":"AUD-7"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/04-multiple-locations.arazzo.yaml bothKinds '{"a":"TRACE-1","b":"META-2"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/05-published-to-wrong-place.arazzo.yaml wrongPlace '{"want":"42"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/06-unsupported-location.arazzo.yaml badLocation '{"want":"42"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/07-targeting-forms.arazzo.yaml everyForm '{"a":"C-1","b":"C-2","c":"C-3"}'
```

```bash
test_runner examples/async_test/phase11/phase11_correlation/08-payload-only-ignores-header.arazzo.yaml payloadWins '{"want":"42"}'
```

Both intentional failures report the same shape, naming the id that found nothing:

```
receive on channel "orders/located" timed out after 2s: no message matching correlationId "42" arrived
```

## What to look for

**In the run log**, scenario 02 announces the imprecision before it happens:

```
Warning: step await: the AsyncAPI document declares no correlationId location for channel
"orders/unlocated", so the whole message is searched for "42" - a message that merely contains
that value elsewhere can match; declare 'correlationId.location' on the channel's message to
match precisely
```

Scenarios 01 and 03 emit no such warning, because the document answered the question.

**In the editor**, the `await` step of scenario 02 carries a blue (information) marker saying the
same thing and naming the source description to edit — `orderBus`. Scenarios 01 and 03 are clean.

## Putting the id in the right place is the AUTHOR's job

Arazzo scopes `correlationId` to **receive** steps, so a send step has no such field and the runtime
has no value it could inject. Scenario 01 places it explicitly with a header parameter:

```yaml
- stepId: emitReal
  action: send
  parameters:
    - name: correlationId
      in: header
      value: $inputs.want
```

If a publisher puts the id somewhere the document does not name, the receive will not find it and the
step times out — which reads exactly like a dead channel. That is the trade for the precision.
