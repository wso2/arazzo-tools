# Phase 8 — AsyncAPI Model Resolution examples

Phase 8 teaches the tooling to **understand** AsyncAPI targets (resolve + navigate + surface info) —
it does **not** send/receive on a broker yet (that is Phase 9). It has three parts, and these
examples cover all three:

| Part | Where | What these examples show |
|---|---|---|
| **1. CLI resolver** | `arazzo-designer-cli` (`asyncapi_finder.go`) | resolve a step's `channelPath` / `operationId` to a channel/operation, its address, and its action |
| **2. LSP** | `arazzo-designer-lsp` | go-to-definition for `channelPath`/`operationId`; validation of async step fields |
| **3. Visualizer** | `arazzo-designer-visualizer` (properties panel) | show `channelPath`/`action`/`correlationId`/`timeout`/`dependsOn` + **Step Type** when a step is clicked |

## Files

| File | Purpose |
|---|---|
| `order-events.asyncapi.yaml` | AsyncAPI 3.0 source — 2 channels (`orders`, `confirmations`) + 2 operations (`placeOrder` send, `onOrderConfirmed` receive) |
| `catalog.openapi.yaml` | tiny OpenAPI source — one `getProducts` op, so a step can be OpenAPI for Step-Type contrast |
| `01-async-properties.arazzo.yaml` | **valid** async flow — the showcase for the **properties panel** and **navigation** |
| `02-async-operationid.arazzo.yaml` | async via **operationId** (bare + scoped) — resolver + operation navigation |
| `03-async-validation.arazzo.yaml` | every step **wrong on purpose** — the **LSP validation** rules |

## How to verify each part

### Part 3 — Properties panel (`01-async-properties.arazzo.yaml`)
Open in the Arazzo visualizer and click each step:
- **browseCatalog** → General shows **Step Type: OpenAPI**; Operation Details shows `getProducts`.
- **sendOrder** → **Step Type: AsyncAPI**; an **AsyncAPI** section (Channel Path, Action, Timeout);
  **Depends On**: browseCatalog.
- **awaitConfirmation** → **AsyncAPI** section incl. **Correlation ID**; **Depends On**: sendOrder.

### Part 2 — LSP navigation (`01` and `02`)
- In `01`, Ctrl/Cmd-click a `channelPath` value → jumps to that channel in `order-events.asyncapi.yaml`.
- In `02`, Ctrl/Cmd-click an `operationId` → jumps to that operation in the AsyncAPI file.

### Part 2 — LSP validation (`03-async-validation.arazzo.yaml`)
Opening it should put a squiggle on every step. Expected diagnostics (verified against the validator):

| Step | Severity | Message |
|---|---|---|
| `noAction` | **error** | a `channelPath` step must also specify `action` (direction undefined) |
| `actionNoChannel` | warning | `action` is only applicable to AsyncAPI steps (needs `channelPath`) |
| `corrOnSend` | warning | `correlationId` is only applicable to steps with action `receive` |
| `wrongSourceType` | **error** | `channelPath` references a source of type `openapi`, must be `asyncapi` |
| `badFormat` | **error** | `channelPath` must be `{sourceDescriptionName}#{channelPath}` |

### Part 1 — CLI resolver (`01` and `02`)
The resolver identifies async targets but **is not wired into execution yet (Phase 9)**, so there is
no `run` for these. It is proven two ways:
- Go unit tests: `asyncapi_finder_test.go`.
- Against these very files: `FindChannelByPath("orderEvents#/channels/orders")` →
  `orders` / `orders/new`; `FindOperationByID("placeOrder")` → `send` / `orders`;
  `$sourceDescriptions.orderEvents.onOrderConfirmed` → `receive` / `confirmations`.

## Notes / current limitations
- **Nothing here executes.** Async send/receive is Phase 9; these files exercise the LSP + visualizer
  + resolver only. (`01`'s single OpenAPI step would call a live API if run, but that's not the point.)
- **Step Type is inferred from a step's fields** (`channelPath` ⇒ AsyncAPI, `operationId`/`operationPath`
  ⇒ OpenAPI, `workflowId` ⇒ Workflow). An async operation referenced by `operationId` (as in `02`)
  therefore shows as **OpenAPI** in the panel — precise classification needs source-type resolution
  (a later enhancement). Use `channelPath` (as in `01`) to see **AsyncAPI**.
- The LSP's `action`/`correlationId` warnings are tied to `channelPath` steps; that's why `02`'s
  operationId steps omit `action` (the operation itself carries the direction, which the resolver reads).
