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
| `03-async-validation.arazzo.yaml` | every step **wrong on purpose** — the **LSP validation** rules for async fields |
| `04-step-targeting-forms.arazzo.yaml` | **every** way a step names its target × **both** source-reference spellings |
| `05-targeting-validation.arazzo.yaml` | targeting mistakes **wrong on purpose** + two spellings that must stay clean |

## How to verify each part

### Part 3 — Properties panel (`01-async-properties.arazzo.yaml`)
Open in the Arazzo visualizer and click each step:
- **browseCatalog** → General shows **Step Type: OpenAPI**; Operation Details shows `getProducts`.
- **sendOrder** → **Step Type: AsyncAPI**; an **AsyncAPI** section (Channel Path, Action, Timeout);
  **Depends On**: browseCatalog.
- **awaitConfirmation** → **AsyncAPI** section incl. **Correlation ID**; **Depends On**: sendOrder.

### Part 2 — LSP navigation (`01`, `02` and `04`)
- In `01`, Ctrl/Cmd-click a `channelPath` value → jumps to that channel in `order-events.asyncapi.yaml`.
- In `02`, Ctrl/Cmd-click an `operationId` → jumps to that operation in the AsyncAPI file.
- In `04`, **every line** is a targeting form: Ctrl/Cmd-click each one and it must land on the
  definition named in its comment. **Hover must name the same file it jumps to.**

### Part 2 — all three targeting forms (`04`)

A step names its target in exactly one of three ways, and the source description before `#` may be
written either way. Both spellings resolve identically:

| Field | Points at | Example (bare) | Example (spec runtime-expression form) |
|---|---|---|---|
| `operationId` | an operation, by name | `getProducts` | `$sourceDescriptions.orderEvents.placeOrder` |
| `channelPath` | an AsyncAPI **channel**, by JSON Pointer | `orderEvents#/channels/orders` | `'{$sourceDescriptions.orderEvents.url}#/channels/orders'` |
| `operationPath` | an operation, by JSON Pointer | `catalog#/paths/~1products/get` | `'{$sourceDescriptions.catalog.url}#/paths/~1products/get'` |

Notes that trip people up:
- **`~1` is an escaped `/`** (JSON Pointer, RFC 6901). `/products` → `~1products`; `~0` is `~`.
- **The runtime-expression form must be quoted** in YAML — a value starting with `{` would otherwise
  be read as a flow mapping.
- The spec *requires* the runtime-expression form for `channelPath`/`operationPath`; the bare source
  name is the common shorthand. **This tooling accepts both**, everywhere — resolution, navigation,
  hover, and validation.
- `operationPath` may address an AsyncAPI operation too (`#/operations/placeOrder`). It exists for
  operations with **no** `operationId`; where one exists the spec says prefer `operationId`.

### Part 2 — LSP validation (`03-async-validation.arazzo.yaml`)
Opening it should put a squiggle on every step. Expected diagnostics (verified against the validator):

| Step | Severity | Message |
|---|---|---|
| `noAction` | **error** | a `channelPath` step must also specify `action` (direction undefined) |
| `actionNoChannel` | warning | `action` is only applicable to AsyncAPI steps (needs `channelPath`) |
| `corrOnSend` | warning | `correlationId` is only applicable to steps with action `receive` |
| `wrongSourceType` | **error** | `channelPath` references a source of type `openapi`, must be `asyncapi` |
| `badFormat` | **error** | `channelPath` must be `<sourceDescription>#<jsonPointer>` |

### Part 2 — targeting validation (`05-targeting-validation.arazzo.yaml`)

Same idea for how a step *names* its target. Expected diagnostics:

| Step | Severity | Message |
|---|---|---|
| `unknownChannelSource` | warning | `channelPath` references unknown source description `ghostBus` |
| `unknownOpPathSource` | warning | `operationPath` references unknown source description `ghostApi` |
| `unknownScopedOpSource` | warning | `operationId` references unknown source description `ghostApi` |
| `badOperationPathFormat` | **error** | `operationPath` must be `<sourceDescription>#<jsonPointer>` |
| `malformedOpIdExpr` | **error** | `operationId` expression must be `$sourceDescriptions.<name>.<operationId>` |
| `operationPathToArazzo` | **error** | use `workflowId` to target an Arazzo workflow |

The `goodTargets` workflow at the bottom uses both legal source-reference spellings and must produce
**no diagnostics at all** — that's the regression guard: neither spelling may be reported as unknown.

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
- **Step Type** is classified as: `channelPath` or `action` ⇒ **AsyncAPI**; `workflowId` ⇒ **Workflow**;
  a **scoped** `operationId` (`$sourceDescriptions.<name>.*`) ⇒ that source's declared type; a **bare**
  `operationId` ⇒ resolved when the document declares exactly one typed source, else **OpenAPI**. So
  `02`'s operationId steps correctly show **AsyncAPI** (single asyncapi source), and `01`'s
  `getProducts` shows **OpenAPI**.
- **Navigation is scoped to the document's `sourceDescriptions`** — an `operationId`/`channelPath`/
  `operationPath` resolves only inside the files this Arazzo doc declares, never into an unrelated
  spec elsewhere in the workspace.
- **Indexing is per-file and happens on open, change, and save.** Opening or editing an Arazzo file
  parses only the sources it declares (resolved against `$self`, spec §5.5 — the same rule the runner
  uses). Saving an Arazzo file re-resolves them, so adding a `sourceDescription` takes effect without
  reopening; saving a source spec re-indexes that file.
- **Source types are tracked per document.** The LSP records each declared source's name, its
  declared `type`, the type the file actually turned out to be, and where it resolved to — keeping
  **AsyncAPI (event) sources separate from OpenAPI/REST ones**. A client can request this via the
  `arazzo/getSourceInfo` LSP method (`{sources, async, rest}`) to tell which kind of API a step targets.
- The LSP's `action`/`correlationId` warnings are tied to `channelPath` steps; that's why `02`'s
  operationId steps omit `action` (the operation itself carries the direction, which the resolver reads).
