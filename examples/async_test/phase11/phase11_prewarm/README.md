# Phase 11 (pre-subscription) — Listening Before The Workflow Starts

Subscription used to be **lazy**: a channel was subscribed to inside the first `Send` or `Receive`
that touched it. That left a window open.

```
t=0.0  workflow starts
t=0.0  step 1: send to A      -> subscribes to A, publishes
t=0.1  step 2: receive from A -> already subscribed, finds it
t=0.2  step 3: receive from B -> subscribes to B *now*
                                 anything published to B before this moment is GONE
```

A broker does not replay what arrived while nobody was listening (MQTT only does that for *retained*
messages, and we publish with `retained: false`). So the runner now walks every step **before step 1**
and subscribes to each channel a step will receive on, shrinking the window to the workflow's own start
— as early as this layer can get.

## The rules

| | |
|---|---|
| **which channels** | every channel some step **receives** on — resolved through `channelPath`, `operationId` *and* `operationPath` |
| **send-only channels** | **not** subscribed. Subscribing fills a buffer and only a receive drains it, so a channel nothing reads would accumulate messages for the whole run with nothing consuming them. Sends still subscribe to their own topic immediately before publishing, so round trips are unaffected |
| **duplicates** | one subscription per channel, however many steps name it |
| **on failure** | a **warning** naming the channel, adapter and step — never fatal. The step that needs the channel retries and fails with its own error |
| **in-memory adapter** | a no-op: its queues exist from the start, so nothing can be missed. The walk still happens, so the log line still appears |
| **WebSocket** | the connection *is* the subscription, so this dials early and starts the reader. A server that greets on connect is therefore greeted at workflow start |

## Scenarios

All four run on the **in-memory adapter — no broker, no network**.

| file | what it shows | expected |
|---|---|---|
| `01-listens-before-first-step` | both received channels are listed *before* any step output, including one not touched until the last step | ✅ `seen = "A-1"`, `reply = "ack"` |
| `02-send-only-channel-skipped` | the audit channel is published to by step 1 and still not subscribed | ✅ `seen = "A-2"`, one channel listed |
| `03-targeting-forms` | the same channel reached by all three targeting forms, subscribed **once** | ✅ `a/b/c = 1/2/3` |
| `04-unreachable-broker` | a broker that cannot be reached: warns first, runs anyway, the step fails | ❌ the step fails, not the warm-up |

```bash
test_runner examples/async_test/phase11_prewarm/01-listens-before-first-step.arazzo.yaml twoChannels
```

```bash
test_runner examples/async_test/phase11_prewarm/02-send-only-channel-skipped.arazzo.yaml sendOnly
```

```bash
test_runner examples/async_test/phase11_prewarm/03-targeting-forms.arazzo.yaml everyForm
```

```bash
test_runner examples/async_test/phase11_prewarm/04-unreachable-broker.arazzo.yaml unreachable
```

## What to look for

The whole subject is one line, and **where it appears**:

```
Listening on 2 channel(s) before the first step: "orders/new", "orders/replies"
=== Executing step: emit ===
```

It comes before any step output. In `02` the list is one channel shorter than the number of channels
the workflow touches; in `03` it is one channel despite four steps naming it.

In `04` the warning arrives in the same position — before step 1 — and names everything needed to act
on it:

```
Warning: could not subscribe to channel "orders/new" via the mqtt adapter before the workflow
started (needed by step await): mqtt connect to tcp://nonexistent.invalid:1883 failed: ... -
the step will try again when it runs, and messages published in the meantime are lost
```

## What an example cannot show

These run in-memory, where pre-subscription is a **no-op** — the queues exist from the start, so no
message can be lost either way. What the examples demonstrate is the *walk*: which channels are
resolved, which are skipped, and when it happens.

The behaviour it actually buys — a message published before the receive step being captured rather
than dropped — needs a broker that discards messages for absent subscribers. That is pinned by
`TestPrewarmCapturesMessagesPublishedBeforeTheReceiveStep`, which drives the fake MQTT broker and
asserts **both** halves: the message is lost without pre-subscription and captured with it.
