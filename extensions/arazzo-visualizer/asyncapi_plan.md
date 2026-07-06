# Arazzo v1.1.0 Support Plan

> **Revision note (audited 2026-06-13).** This plan was originally written as if the
> repository had *no* v1.1.0 support. A full code + spec audit shows that is no longer
> true: the **data-model layer, LSP validation/completion, syntax highlighting, and test
> fixtures for v1.1.0 are already implemented and present in the repo.** The remaining
> work is almost entirely in the **runtime/execution layer** (the Go CLI runner + evaluator)
> and the **visualizer**. This revision corrects the false "everything is missing" premise,
> records what is actually done, and rescopes each phase to the real remaining work.
>
> Three specific claims in the original plan were verified **FALSE** and have been removed:
> 1. *"TypeScript `CriterionExpressionObject` has a bug naming the field `expression` instead of `version`."* — There is no such interface anymore; it is correctly named `ExpressionTypeObject` with a `version` field ([arazzoInterface.ts:186](arazzo-designer-core/src/interfaces/arazzoInterface.ts)). No lingering `CriterionExpression` name exists in core, LSP, or CLI.
> 2. *"LSP `RequestBody` struct is missing `Replacements`."* — It is present ([parser/ast.go:75](arazzo-designer-lsp/parser/ast.go)).
> 3. *"`$self`, `channelPath`, `timeout`, `correlationId`, `action`, step-level `dependsOn`, `querystring`, and action `parameters` are missing."* — All are present in TypeScript, LSP, and CLI models with explicit `// v1.1.0` annotations.

## Summary

Deliver full Arazzo `1.1.0` support across the product: shared TypeScript models,
Go LSP (parser/validator/completion/navigation), visualizer, CLI/MCP runner, and tests —
while preserving `1.0.0` / `1.0.1` compatibility.

Sources checked:
- Arazzo v1.1.0 (latest, dated 17 May 2026): https://spec.openapis.org/arazzo/latest.html
- Arazzo v1.0.1: https://spec.openapis.org/arazzo/v1.0.1.html

The **authoring/static** half of v1.1.0 is done. The **executing/dynamic** half is not.
Concretely, the runtime does not yet: resolve `$self`/base-URI references, evaluate Selector
Objects, evaluate the new runtime-expression roots (`$message`, `$self`, `$sourceDescriptions`,
`$components`) or compound boolean criteria, perform JSONPath/XPath payload replacement, honor
step `dependsOn` ordering, or execute any AsyncAPI `send`/`receive` step. The visualizer does
not yet render async metadata or dependency edges.

---

## Current Implementation Status (Audit — 2026-06-13)

Legend: ✅ done · 🟡 partial · ❌ not started

### Model / schema layer — ✅ DONE
| Item | TS core | LSP `ast.go` | CLI `models` |
|---|---|---|---|
| `arazzo: "1.1.0"` accepted | ✅ [arazzoInterface.ts:21](arazzo-designer-core/src/interfaces/arazzoInterface.ts) | ✅ | ✅ [arazzo.go:6](../../arazzo-designer-cli/internal/models/arazzo.go) |
| Root `$self` | ✅ | ✅ `Self` | ✅ `Self` |
| `sourceDescriptions.type` incl. `asyncapi` | ✅ | ✅ | ✅ |
| Step `channelPath` / `action` / `correlationId` / `timeout` / `dependsOn` | ✅ | ✅ | ✅ |
| Parameter `in: querystring` | ✅ | ✅ | ✅ |
| `SuccessAction`/`FailureAction` `parameters` | ✅ | ✅ | ✅ (`Action.Parameters`) |
| `ExpressionTypeObject` (renamed, `version` field, `jsonpointer`) | ✅ | ✅ (via `interface{}`) | ✅ (via `interface{}`) |
| `SelectorObject` type | ✅ | ✅ | 🟡 (modeled as `interface{}`, no dedicated struct) |
| `RequestBody.replacements` + `PayloadReplacementObject.targetSelectorType` | ✅ | ✅ | ✅ |
| Widened `outputs` / `value` to accept Selector Objects | ✅ | ✅ | ✅ |

### LSP authoring layer — ✅ DONE (verify-only)
- Version validation accepts `1.0.0`/`1.0.1`/`1.1.0` ([validator.go:75](arazzo-designer-lsp/validator/validator.go)).
- Completions for new fields/enums (`$self`, `asyncapi`, `channelPath`, `action`→`send`/`receive`, `timeout`, `correlationId`, `dependsOn`, `querystring`, all three versions).
- Validations implemented in [validator.go](arazzo-designer-lsp/validator/validator.go):
  - `validateSelf` — `$self` MUST NOT contain a fragment (`:428`).
  - `validateDependsOn` — three reference forms (`:481`).
  - `action` enum `send`/`receive`; `channelPath` mutual exclusivity & AsyncAPI source check.
  - `timeout` non-negative integer (`:304`).
  - `correlationId` warning when no `channelPath` (`:317`).
  - **Empty `successCriteria` rejected** (`:326`).
  - `validateActionParameters` — `in` MUST NOT be used on action params (`:602`, called `:340`/`:347`).
  - `validateComponentKeys` — `^[a-zA-Z0-9\.\-_]+$` (`:444`).
- Syntax highlighting (`arazzo.tmLanguage.json`) highlights the new keywords (per `examples/async_test/README.md`).
- Test fixtures exist: [examples/async_test/](../../examples/async_test) (`v110-openapi-new-fields`, `v110-asyncapi-channel`, `v101-backward-compat`, `invalid-v110`).

**Phase 1 & 2 verification pass (completed 2026-06-13).** A full correctness audit was run and
the gaps it found were fixed:
- Completion: `type:` value completion is now context-aware (source vs action vs criterion);
  removed the invalid `body` parameter location; added a root `$self` field completion.
- Validation added: `action` requires `channelPath`; `correlationId` requires `action: receive`;
  parameter `in` enum; success/failure action `type` enum + `stepId`/`workflowId` exclusivity;
  reusable-object `reference` resolution against `components`.
- New **unknown-field detection** ([validator/unknown_fields.go](arazzo-designer-lsp/validator/unknown_fields.go)) walks the YAML tree and warns on
  misspelled/unrecognized keys (e.g. `chanelPath`, or OpenAPI `$ref` where Arazzo wants `reference`),
  allowing `x-` extensions. Wired into diagnostics.
- Fixtures corrected: `$ref` → `reference`; removed a spec-invalid `correlationId` on a `send`
  step and a bogus `$now` expression.
- `main.go` banner now advertises 1.1.0.
- Added Go unit tests: [validator/validator_test.go](arazzo-designer-lsp/validator/validator_test.go) (version/selector/action/dependsOn/`$self`/
  source-type/`in`/reference/unknown-field cases + a fixtures test asserting zero diagnostics on
  the valid files) and [parser/parser_test.go](arazzo-designer-lsp/parser/parser_test.go) (v1.1.0 field round-trip + backward compat).
  `go build`/`go vet`/`go test ./...` all green.

**Remaining minor gaps (acceptable, fold into later phases):**
- `validateRuntimeExpressions` only checks `$steps`/`$workflows` roots ([validator.go](arazzo-designer-lsp/validator/validator.go)); it
  does not yet know `$message`/`$self`/`$sourceDescriptions`/`$components` (tighten in Phase 5).
- Workflow-level and component-level action/parameter checks are lighter than step-level (the
  unknown-field pass still catches typos there). `ExpressionTypeObject.version` validation is
  deferred to Phase 4 by design.

### Runtime / execution layer (Go CLI) — ❌ MOSTLY NOT STARTED
- Evaluator ([evaluator.go](../../arazzo-designer-cli/internal/evaluator/evaluator.go)) supports `$statusCode`, `$response.header/.body`, `$response`, `$inputs`, `$steps`, `$dependencies`, and JSON-Pointer (`#/…`). Comments at `:112‑114` explicitly mark `$workflows`/`$url`/`$method`/`$components` as **not implemented**. No `$message`, `$self`, or `$sourceDescriptions.<name>.<id>` resolution. Boolean criteria support only the six comparison operators (`==,!=,>,<,>=,<=`); **no `&&`/`||`/`!`/parentheses**.
- Loader ([loader.go](../../arazzo-designer-cli/internal/loader/loader.go)) has **no `$self` / base-URI / RFC3986 resolution** logic.
- Selector Objects in outputs are **preserved as-is, not evaluated** ([output_extractor.go:67](../../arazzo-designer-cli/internal/runner/executor/output_extractor.go)). No shared selector-evaluation service exists.
- Payload replacement supports **JSON Pointer targets only** via `setJSONPointer` ([parameter_processor.go:236](../../arazzo-designer-cli/internal/runner/executor/parameter_processor.go)); no JSONPath/XPath targets, no Selector-Object values.
- Runner ([runner.go](../../arazzo-designer-cli/internal/runner/runner.go)) executes steps **sequentially**; it consumes only the `goto`/`end`/`retry`/`continue` *action types* and **never reads `Step.Action`, `Step.ChannelPath`, `Step.CorrelationID`, `Step.DependsOn`, or `Step.Timeout`.** No AsyncAPI execution, no adapter/serialization/broker code anywhere in the repo.

### Visualizer — 🟡 PARTIAL
- Renders `goto`/`retry`/`end` action edges and workflow-level `dependsOn` ([InitVisitor_v2.ts](arazzo-designer-visualizer/src/visitors/InitVisitor_v2.ts), [WorkflowView.tsx:305](arazzo-designer-visualizer/src/views/WorkflowView/WorkflowView.tsx)).
- **No** rendering for async metadata (`channelPath`, `action`, `correlationId`, `timeout`), `$self`/source-type badges, distinct `send`/`receive` nodes, or **step-level** `dependsOn` edges.

---

## Implementation Phases

> Phases 1–2 are **already implemented**; they are retained as **verification checklists**
> so an implementing AI confirms (and does not re-create) the existing work, then closes the
> small documented gaps. Phases 3–12 are the **real remaining work**.

### Phase 1: Version, Schema, And Compatibility Foundation — ✅ DONE (verify-only)

Goal: the repo understands the v1.1.0 document shape without changing execution behavior.

**Already implemented** (see audit above): all three model layers accept `1.1.0`, carry
`$self`, `asyncapi`, `channelPath`, `timeout`, `correlationId`, `action`, step `dependsOn`,
`querystring`, action `parameters`, `ExpressionTypeObject`, `SelectorObject`,
`PayloadReplacementObject.targetSelectorType`, and widened `outputs`/`value` types.

Verification tasks (no new modeling expected):
- Confirm the three model layers stay structurally aligned (TS ↔ LSP ↔ CLI). The one
  intentional divergence to keep in mind: the CLI models Selector/Expression-Type objects
  as `interface{}` rather than typed structs — fine for parsing, but Phase 4/5 will need a
  typed shape or a decode helper.
- Confirm `1.0.0`/`1.0.1` fixtures still parse and the `invalid-v110` fixture still fails.
- Confirm a step with two of `operationId`/`operationPath`/`channelPath`/`workflowId` is rejected.

Exit check: a v1.1.0 file opens without breakage (already true via `examples/async_test`).

### Phase 2: LSP Authoring Support — ✅ DONE (verify + close minor gaps)

Goal: v1.1.0 is pleasant and correct to author in VS Code.

**Already implemented**: completions and validations enumerated in the audit
(`validateSelf`, `validateDependsOn`, `action` enum, `timeout`, `correlationId`,
empty-`successCriteria`, `validateActionParameters`, `validateComponentKeys`).

Remaining small tasks — **completed in the 2026-06-13 verification pass** (see audit section
above): `main.go` banner updated; completion/validation gaps closed; unknown-field detection
added; unit tests added. The only deliberately-deferred item is tightening
`validateRuntimeExpressions` for the new expression roots, which belongs with Phase 5.

Verification tests (most already exist under `examples/async_test`): bad `$self` (fragment),
bad `action`, negative `timeout`, invalid source `type`, duplicate target selectors,
empty `successCriteria`, action-param with `in`, `dependsOn` cross-workflow form.

### Phase 3: `$self` And Source Resolution — ✅ DONE (2026-06-13)

Goal: correctly resolve source documents in v1.1.0.

**Implemented.** Source resolution actually happens in exactly one place at runtime — the CLI
loader ([loader.go](../../arazzo-designer-cli/internal/loader/loader.go), called only from `runner.go`). Added [internal/loader/resolve.go](../../arazzo-designer-cli/internal/loader/resolve.go) with
pure, unit-tested functions: `IsRemoteURL`, `ResolveBaseURI(self, retrievalPath)`, and
`ResolveSourceLocation(baseURI, sourceURL)`. `LoadSourceDescriptions` now derives the base URI
from `$self` and resolves each relative `sourceDescriptions.url` against it; an absolute `$self`
turns relative sources into remote URLs that are fetched. Identical resolved locations are loaded
once (identity-over-location dedup). When `$self` is absent the retrieval directory is the base,
so **v1.0.x behavior is byte-for-byte preserved**. Tests: [resolve_test.go](../../arazzo-designer-cli/internal/loader/resolve_test.go) (base-URI &
source-location cases) + [loader_test.go](../../arazzo-designer-cli/internal/loader/loader_test.go) (local, relative-`$self`, remote-`$self` via httptest,
dedup, absolute-remote-source) — all green; full CLI suite green.

The other two layers named below need no change: the LSP parses the whole document before use
and discovers OpenAPI files by directory scan (it never resolves `sourceDescriptions.url`), and
the RPC client does no URL resolution. Spec §5.5.1 (full parse before resolution) already holds
in the CLI (`LoadArazzoDoc` parses everything before `LoadSourceDescriptions` runs).

**Scope note / deferred:** the deep form of §5.5.2 identity matching — loading an *external Arazzo
document* and matching a cross-document workflow reference against the *target's* declared `$self`
— is not wired up, because the runner does not yet execute workflows in external Arazzo documents.
That belongs with cross-document workflow execution (overlaps Phase 7's cross-document `dependsOn`)
and should be implemented there. The loader-level base-URI resolution and same-location dedup that
Phase 3 needs are done.

Original spec-derived requirements (for reference):
- Enforce full-document parse before any reference resolution (spec §5.5.1) in the LSP loader,
  CLI loader, and RPC client.
- Establish the base URI per RFC3986 §5.1.1–5.1.4 priority:
  - `$self` absolute → use directly as base URI.
  - `$self` relative → resolve against the next base source (retrieval URI / encapsulating
    entity / application default), then use the resulting absolute URI.
  - `$self` absent → use the retrieval URI (file path or HTTP URL) as base URI.
- Resolve relative `sourceDescriptions.url` against that base URI.
- Identity-based matching for external Arazzo refs: if the target has `$self`, the reference
  MUST match the `$self` URI, not just the retrieval location (spec §5.5.2). See spec
  Appendix B ("Examples of Base URI Determination and Reference Resolution") for worked cases.
- Preserve current local-relative behavior for v1.0.x (no `$self` → file path is base URI).

Tests: relative source with `$self` absent; with absolute `$self`; relative `$self` resolved
against retrieval URI; remote-style `$self` + relative source URL; two docs sharing a `$self`
treated as one; full parse before resolution; existing examples still load.

### Phase 4: Selector Objects And Expression Types — ❌ NOT STARTED

Goal: support the new structured-extraction model. **Currently Selector Objects are preserved
verbatim and never evaluated.**

Changes:
- Add a typed `SelectorObject` decode (`context`, `selector`, `type`) on the CLI side (today
  it is `interface{}`), plus an `ExpressionTypeObject` decode (`type` REQUIRED, `version`
  REQUIRED — reject the object if `version` is omitted).
  - Allowed `version` per `type`: `jsonpath` → `rfc9535` | `draft-goessner-dispatch-jsonpath-00`;
    `xpath` → `xpath-31` | `xpath-30` | `xpath-20` | `xpath-10`; `jsonpointer` → `rfc6901`.
  - When the Expression-Type Object is **absent**, tooling applies defaults
    (`jsonpath`→`rfc9535`, `xpath`→`xpath-31`, `jsonpointer`→`rfc6901`). These are *tooling*
    defaults, not object defaults — the object itself always requires both fields.
- Build a **shared selector-evaluation service** used by all runner components:
  - `jsonpointer` → RFC6901 (reuse existing `ResolveJSONPointer`).
  - `jsonpath` → reuse the existing `ojg` RFC9535 JSONPath ([jsonpath.go](../../arazzo-designer-cli/internal/evaluator/jsonpath.go)).
  - `xpath` → add XML/XPath support behind the same service.
  - Unsupported type/version → clear validation/runtime error.
- Wire Selector Objects everywhere v1.1.0 permits: workflow outputs, step outputs, parameter
  values, request-body payload values, payload replacement values (replacing the current
  "preserve as-is" path in `output_extractor.go`).
- Keep existing string runtime-expression behavior intact.

Tests: selector from `$response.body`; from `$message.payload`; JSON Pointer; JSONPath; XPath
on XML; unsupported type/version fails clearly.

### Phase 5: Runtime Expression Upgrade — ❌ NOT STARTED

Goal: bring the evaluator to v1.1.0. **Current evaluator lacks `$message`/`$self`/`$sourceDescriptions`/`$components` and compound boolean criteria.**

Changes:
- Add expression roots to [evaluator.go](../../arazzo-designer-cli/internal/evaluator/evaluator.go):
  - `$message.header.*`, `$message.payload`, `$message.payload#/…`
  - `$self`
  - `$sourceDescriptions.<name>.<id>` — implement the two-step priority of spec §5.9.2:
    (1) match `<id>` against an `operationId`/`workflowId` in the named source; (2) only if no
    match, treat `<id>` as a field of the Source Description Object (e.g. `url`). Implement the
    priority explicitly; do not allow ambiguous resolution.
  - `$components.successActions.*`, `$components.failureActions.*`
  - (also fill the already-stubbed `$workflows`/`$url`/`$method` noted at `evaluator.go:112`)
- Embedded-expression serialization: primitives embed as strings; objects/arrays serialize
  consistently (normally JSON); unresolved expressions produce useful, context-aware warnings.
- Replace the simple comparison parser with a real expression evaluator supporting
  `!`, `&&`, `||`, parentheses, property dereference, array indexing, numeric & string
  comparison, and case-insensitive comparison where the spec requires it.

Tests: `$message.payload.status == "confirmed"`; `$message.header.correlationId`; `$self`
resolves; `$sourceDescriptions.petstore.url` resolves; object/array embedded serialization;
compound criteria with `&&`/`||`/`!`/parentheses/indexing.

### Phase 6: Payload Replacement Upgrade — ❌ NOT STARTED

Goal: support v1.1.0 replacement targets/values. **Currently JSON-Pointer targets only (`setJSONPointer`).**

Changes:
- Extend replacement with `targetSelectorType` and JSONPath / XPath / JSON-Pointer targets.
- Allow Selector-Object replacement values.
- Preserve existing JSON-Pointer replacement behavior.
- Route target lookup and value resolution through the Phase 4 shared selector service.

Tests: existing JSON-Pointer replacement still works; JSONPath target; XPath target on XML;
replacement value as literal / runtime expression / Selector Object.

### Phase 7: OpenAPI Runtime Preservation And Step Dependencies — ❌ NOT STARTED

Goal: dependency-aware ordering without breaking current REST flows. **Runner currently ignores `Step.DependsOn` and runs steps sequentially.**

Changes:
- Keep current sequential execution when no dependencies are declared.
- Honor explicit step `dependsOn`; infer dependencies from `$steps.<id>.outputs.*` where safe.
- Detect impossible graphs: missing step id (local, `$workflows.*` cross-workflow,
  `$sourceDescriptions.*` cross-document), cycles, never-completing deps.
- `dependsOn` is a **prerequisite relationship only** — it does not *trigger* the referenced
  step and must not re-execute an already-completed prerequisite; the runner waits if needed.
- Keep `onSuccess`/`onFailure`/`goto`/`end`/`retry` behavior compatible; clarify precedence
  between retry exhaustion and following failure actions.

Tests: existing OpenAPI examples still run; explicit `dependsOn` waits; cross-workflow
`$workflows.<wf>.steps.<s>` resolves & waits; completed step not re-executed; implicit
`$steps.x.outputs.y` dependency respected; cycle fails clearly; retry still works.

### Phase 8: AsyncAPI Model Resolution And Visualization — ❌ NOT STARTED

Goal: understand AsyncAPI sources and show them before real broker execution.

Changes:
- Load AsyncAPI source docs from `sourceDescriptions`; index operations & channels.
- Resolve AsyncAPI refs from `operationId`, scoped ids (`$sourceDescriptions.orderEvents.placeOrder`),
  and `channelPath`.
- Navigation: keep OpenAPI op nav; add AsyncAPI operation nav and `channelPath` channel nav.
- Visualizer ([arazzo-designer-visualizer](arazzo-designer-visualizer)):
  - show `$self` in overview; source-type badges (OpenAPI / AsyncAPI / Arazzo).
  - render `send`/`receive` steps distinctly.
  - show `channelPath`/`action`/`correlationId`/`timeout`/`dependsOn` in the properties panel.
  - draw **step-level** `dependsOn` edges (workflow-level already drawn).

Tests: AsyncAPI source loads; op/channel indexed; `channelPath` navigation resolves; async
metadata shown; dependency edges render without breaking success/failure/goto edges.

### Phase 9: AsyncAPI Adapter Interface — ❌ NOT STARTED

Goal: a runtime boundary to send/receive messages without hard-coding any broker.

Key idea: the runner implements **adapters/connectors**, not brokers. Kafka/RabbitMQ/MQTT/NATS/
WebSocket/cloud queues are external systems.

Core interface: `Send(channel, message, options)` and
`Receive(channel, correlationId, timeout, options)`, returning a normalized message
(headers, payload, raw bytes, content type, metadata like topic/queue/channel).

Runner behavior:
- `action: send` → resolve op/channel, evaluate params & body, serialize, `Send`, store send metadata.
- `action: receive` → resolve op/channel, evaluate `correlationId`, `Receive`, enforce
  `timeout`, expose `$message`, evaluate `successCriteria`, extract outputs from `$message`.

Initial adapters: in-memory/test adapter; clear error when a real broker adapter is required
but unconfigured: `AsyncAPI execution requires a configured adapter for this protocol`.

Tests: in-memory send; in-memory receive matches; receive ignores non-matching correlation ids;
receive times out; `$message.payload` criteria & outputs work.

### Phase 10: Message Serialization Layer — ❌ NOT STARTED

Goal: separate message shape from wire format so adapters don't each reinvent serialization.

Architecture: step builds a logical message (headers/payload/correlationId/contentType) →
serializer → bytes/string → adapter Send/Receive → deserializer → `$message.payload`.

Initial: JSON first; plain text if trivial; binary passthrough only when explicitly configured.
Future: Avro, Protobuf, CloudEvents, custom — via a **serializer registry** keyed by content
type / AsyncAPI message binding (`application/json`→JSON, `text/plain`→text,
`application/x-protobuf`→Protobuf, `application/avro`→Avro). Protobuf needs `.proto`/descriptor
info; Avro needs a schema/registry — both are adapter/serializer config, and runtime should
fail clearly rather than guess when schema info is absent.

Tests: JSON send serializes; JSON receive deserializes into `$message.payload`; unsupported
content type fails clearly; registry selects the right serializer; placeholder tests document
Protobuf/Avro expectations until implemented.

### Phase 11: First Real Broker Adapter — ❌ NOT STARTED

Goal: one production-grade adapter after the generic runtime is proven. Candidates: WebSocket
(easiest demo), Kafka (enterprise streaming), MQTT (IoT). Responsibilities per adapter: map
AsyncAPI channel → topic/exchange, publish & consume, match `correlationId` from headers/payload,
use the serializer registry, support auth/TLS (and consumer groups / QoS / routing keys as
applicable).

Tests: integration tests behind opt-in env vars; unit tests with mocked broker clients;
end-to-end sample for the chosen broker.

### Phase 12: CLI, MCP, Documentation, And Samples — ❌ NOT STARTED (partial samples exist)

Goal: make the feature usable and explainable.

Changes:
- CLI workflow details: Arazzo version, `$self`, source types, async channel/action metadata,
  adapter-config status.
- MCP responses: include async metadata; surface unsupported-adapter and timeout/correlation
  errors clearly.
- Examples: minimal v1.1.0 OpenAPI-only; v1.1.0 AsyncAPI send/receive on in-memory adapter;
  selector-object examples; JSONPath/XPath replacement examples; a real-broker example once one
  exists. (Note: `examples/async_test` already holds Phase-1 *parsing* fixtures — extend rather
  than duplicate.)
- Docs: REST vs AsyncAPI; `send`/`receive`; channels; broker vs adapter; serializer.

Tests: CLI still lists/runs old workflows; CLI reports async adapter errors clearly; MCP output
stable for old workflows; new examples parse and validate.

---

## Final Acceptance Criteria

- `arazzo: 1.1.0` accepted everywhere; `1.0.0`/`1.0.1` still work. *(model layer ✅)*
- `$self` and v1.1.0 source resolution work. *(model ✅ / runtime ❌)*
- `asyncapi` sources load and resolve.
- `channelPath`/`action`/`correlationId`/`timeout`/`dependsOn`/`querystring` modeled, validated,
  completed *(✅)*, and **executed/visualized** *(❌)*.
- Selector Objects & Expression Type Objects work in supported locations *(modeled ✅ / evaluated ❌)*.
- JSONPath, XPath, JSON Pointer selectors/replacements supported.
- `$message` expressions work for AsyncAPI receive steps.
- OpenAPI execution remains stable.
- AsyncAPI execution works through the in-memory/test adapter; missing-real-broker errors are clear.
- Serialization separated from adapters, JSON first; Protobuf/Avro via registry, not hard-coded.

## Assumptions

- Scope is full-product support: editor, validation, visualization, CLI runner, MCP, tests.
- AsyncAPI execution is pluggable, not hard-coded to a specific broker initially.
- Real broker adapters are added incrementally after the adapter interface + serialization are stable.
- JSON is the first serialization format; Protobuf/Avro/CloudEvents/custom are follow-ups.
- Existing `1.0.0`/`1.0.1` behavior stays backward compatible.

## Suggested Execution Order

The model/LSP work (Phases 1–2) is done, so an implementing AI should start at **Phase 3** and
proceed 3 → 12. Phases 4 and 5 share the selector/expression service and are best done together;
Phase 6 depends on Phase 4; Phase 7 is independent and can be parallelized with 4–6; Phases
8–11 form the AsyncAPI runtime track and depend on 3 (resolution) + 4–5 (evaluation) + 9
(adapter) before 10–11. Phase 12 closes out docs/samples last.
