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

### Phase 4: Selector Objects And Expression Types — ✅ DONE except XPath (2026-06-17)

**Implemented.** New shared selector-evaluation service [internal/evaluator/selector.go](../../arazzo-designer-cli/internal/evaluator/selector.go):
`IsSelectorObject` (detects a `{context, selector, type}` map), `EvaluateSelectorObject`
(resolves the `context` expression, then routes by dialect), `resolveExpressionType` (handles
bare-string or Expression Type Object `type`, applies default versions, rejects unknown
dialects/versions), and `EvaluateJSONPathValue` (value-returning JSONPath, complementing the
existing bool criterion engine). Wired into all permitted spots: step outputs
([output_extractor.go](../../arazzo-designer-cli/internal/runner/executor/output_extractor.go)), workflow outputs ([runner.go](../../arazzo-designer-cli/internal/runner/runner.go) `resolveWorkflowOutputs`),
parameter values + request-body payloads (nested) + payload replacement values
([parameter_processor.go](../../arazzo-designer-cli/internal/runner/executor/parameter_processor.go)), and the central `processValue` recursion. `jsonpointer` reuses
`ResolveJSONPointer` (RFC 6901); `jsonpath` reuses the `ojg` engine (RFC 9535). **XPath returns
a clear "not yet supported" error** (deferred to the next step). LSP: `validateExpressionType`
([validator.go](arazzo-designer-lsp/validator/validator.go)) validates Expression Type Objects on criterion `type` (version required + valid per
type). Plain string expressions are untouched. Tests: `evaluator/selector_test.go`,
`executor/output_extractor_test.go`, `executor/parameter_processor_test.go`, and an LSP
`TestExpressionType` — all green; full CLI + LSP suites green.

**Remaining for the XPath follow-up (do near the end, together with Phase 6's `targetSelectorType`):**
add an XML/XPath engine and route `xpath` selectors to it (currently they error). The **same XPath
engine also unblocks Phase 6's XPath replacement targets**, and `targetSelectorType` (JSONPath/XPath
replacement targets) is likewise deferred there — so XPath-selectors (this phase) and the replacement
target side (Phase 6) are best done as one final XPath/`targetSelectorType` push. Also a possible
tightening: LSP validation of Expression Type Objects on Selector Objects / `targetSelectorType`
inside outputs/payloads (currently runtime-validated only, since those are untyped maps in the LSP).

Original spec-derived requirements (for reference):

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

### Phase 5: Runtime Expression Upgrade — ✅ DONE (branch `asyncV1-phase5`)

Implemented in `internal/evaluator/evaluator.go` (+ `internal/models/models.go`, `internal/runner/runner.go`):
- **New expression roots (spec §5.9):** `$self`; `$message.header.*` / `$message.payload[#/…]` (AsyncAPI,
  resolves from the evaluation context — nil until an async runtime populates it); `$components.<type>.<name>`;
  `$workflows.<id>.<field>`; `$url` / `$method` (from context).
- **`$sourceDescriptions.<name>.<ref>` with the §5.9.2 two-step priority:** match `<ref>` against an
  `operationId` (OpenAPI/AsyncAPI) or `workflowId` (Arazzo) in the referenced doc first; only on no match,
  treat `<ref>` as a Source Description Object field (e.g. `url`, `type`). Source kind comes from the SD
  Object's `type` (fallback: the spec's marker key). NOTE: this is the **general expression** form; the
  operation-targeting forms in `operationId` / `operationPath` were already handled by `operation_finder.go`.
- **Compound boolean criteria:** `EvaluateSimpleCondition` is now a quote-aware recursive-descent evaluator
  supporting `!`, `&&`, `||`, parentheses, plus the existing comparisons / property-deref / array-indexing
  (operands run through the full expression evaluator). Signature unchanged, so all callers are untouched.
- **Embedded `{$…}` serialization:** primitives embed as text; objects/arrays embed as JSON; unresolved
  placeholders are left in place with a context-aware warning.
- **State threading:** `ExecutionState` gains `Self`, `Components`, `SourceDescriptionObjects`, `WorkflowsByID`,
  populated by the runner from the Arazzo document.
- **LSP:** no change needed — `validateRuntimeExpressions` only special-cases `steps`/`workflows` (no default
  branch), so the new roots are not flagged.
- **Tests:** `internal/evaluator/evaluator_phase5_test.go` (all roots, §5.9.2 priority, compound/grouped
  conditions, embedded JSON). Build + vet + full suites green on both modules.
- **Deferred / not done:** case-insensitive string comparison (no clear spec requirement located; left
  case-sensitive — revisit if the spec mandates it for a specific operator).

Goal (original): bring the evaluator to v1.1.0. **Previously the evaluator lacked `$message`/`$self`/`$sourceDescriptions`(general)/`$components` and compound boolean criteria.**

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

### Phase 6: Payload Replacement Upgrade — ✅ DONE except XPath (deferred to the end-of-project XPath push)

Status:
- **Value side — ✅ done (Phase 4):** a replacement `value` can be a literal, a runtime expression,
  or a Selector Object; `applyReplacements` evaluates it.
- **Target side — ✅ done for JSON Pointer + JSONPath:** `applyReplacements` now reads
  `targetSelectorType` (string or Expression Type Object, via `evaluator.ResolveExpressionType`) and
  routes the `target` accordingly — **JSON Pointer** (`setJSONPointer`, the default when omitted) and
  **JSONPath** (`evaluator.SetJSONPath`, backed by `ojg`'s `Set`). **JSON Pointer targets support array
  indices** (e.g. `/items/0/product_id`, mirroring the read side) and a failed/nil replacement value
  is skipped rather than injecting garbage. Tests: `evaluator.TestSetJSONPath`,
  `executor.TestApplyReplacements_JSONPathTarget`, `executor.TestApplyReplacements_JSONPointerArrayIndex`;
  examples `phase4_selectors/07-jsonpath-replacement-target` and `08-jsonpointer-array-target`.
- **Target side — ❌ XPath only:** an `xpath` `targetSelectorType` logs a clear "not yet supported"
  warning. This is the **only** remaining replacement gap and it **depends on the XPath engine that
  Phase 4 also deferred** — so finish it together with the Phase 4 XPath selectors as one final XPath
  push (see the "End-of-project cleanup batch" note under Known Issues / Bugs).

**Remaining for the XPath follow-up:**
- Add the XML/XPath engine and route `xpath` replacement targets (and `xpath` selectors from Phase 4) to it.
- Apply the XML default: when `targetSelectorType` is omitted and the payload is XML, treat the target as XPath.
  (JSON default — JSON Pointer — is already in place.)
- Optional: LSP validation of `targetSelectorType` as an Expression Type Object (version required + valid per type).

### Phase 7: Step Dependencies (`dependsOn`) — ✅ DONE

**Implemented (matches the design below):**
- **Runtime step gate** ([runner.go](arazzo-designer-cli/internal/runner/runner.go) `checkStepDependencies`) — checked before each step; no reordering, no triggering; hard error on an unmet prerequisite (spec §5.8.5.1). Reference forms: local `stepId`; `$workflows.<wf>.steps.<s>`; `$sourceDescriptions.<name>.<wf>.steps.<s>` (form-validated, execution deferred).
- **Cross-workflow step granularity** — the gate verifies the **specific** referenced step reached success (not just that the workflow ran). The runner now surfaces each dependency workflow's per-step status (`WorkflowExecutionResult.StepsStatus` → `ExecutionState.DependencyStepStatus`), so a step skipped via `goto` in a dependency correctly fails the gate.
- **Workflow-level `dependsOn` cycle guard** ([runner.go](arazzo-designer-cli/internal/runner/runner.go) `executeDependencies` `depStack`) — a circular workflow dep now errors clearly instead of stack-overflowing (trigger behavior kept).
- **LSP static validation** ([validator.go](arazzo-designer-lsp/validator/validator.go)) — `dependsOn` reference forms + existence + self-reference, plus **cycle detection** for both **step-level** (`validateDependsOnCycles`) and **workflow-level** (`validateWorkflowDependsOnCycles`) `dependsOn`.
- **Examples**: [examples/async_test/phase7/](examples/async_test/phase7) 01–08 (gate satisfied/unmet, workflow dep, step & workflow cycles, cross-workflow dep, cross-workflow specific-step-ran vs step-skipped-by-goto). Unit tests: `runner_phase7_test.go`, `validator_test.go`.
- **Deferred (unchanged):** async wait-with-timeout (Phases 8–11); cross-**document** `dependsOn` execution (end-of-project batch); the **visualization** of `dependsOn` edges / blocked-step state (Phase 13, needs team sign-off).

<details><summary>Original design (as implemented) — kept for reference</summary>

**Runner currently ignores `Step.DependsOn`** (parsed into the model at `models/arazzo.go`, never read by the
execution loop) and runs steps in document order + control flow.

**Design decision (grounded in spec §5.8.5.1):** `dependsOn` is a **completion GATE**, not a reordering
directive and not a trigger. The spec: *"A list of steps that MUST be completed before this step can be
executed. `dependsOn` only establishes a prerequisite relationship … and does not trigger execution of the
referenced steps,"* and it is intended primarily for *non-blocking/asynchronous* steps. Therefore:
- **NO reordering, NO model mutation, NO triggering.** The runner keeps executing in the existing order
  (document order + `goto`/`onSuccess`/`onFailure`/`retry`, all unchanged). It only adds a *gate check*
  before each step.
- **"Completed" = the prerequisite step ran and reached terminal SUCCESS.** A prerequisite that ran but
  failed does NOT satisfy the gate.

**Runtime gate (CLI), before executing each step:**
- **REST / OpenAPI (synchronous):** a prerequisite is either done or not. If a `dependsOn` step has not
  completed → **HARD ERROR**: mark this step failed and fail the workflow with a clear message naming the
  step and the unmet dependency. No waiting/timeout.
- **AsyncAPI (async / non-blocking):** only if a prerequisite **started but hasn't reported completion yet**,
  **wait up to a hardcoded timeout** for it to complete → proceed on completion, else hard error. A
  prerequisite that **never started** is an immediate hard error (no waiting). *This wait branch is designed
  here but WIRED with the AsyncAPI runtime (Phases 8–11) — no async step executes before then, so Phase 7's
  implemented deliverable is the synchronous gate + LSP validation.*
- **Reference forms:** local `stepId` → full runtime gate; `$workflows.<wf>.steps.<s>` (cross-workflow) →
  check that step's completion if the workflow ran, else error; `$sourceDescriptions.<name>.<wf>.steps.<s>`
  (cross-document) → **validate the form only**; execution rides with the deferred external-Arazzo-source batch.

**LSP static validation** (catch at authoring time — extends the existing `dependsOn` checks that already
validate the reference forms + local step existence):
- missing / unknown `stepId` → error (partly exists).
- **cycles** (A→B→A) → error.
- a dependency that **cannot have completed** by document/flow order (e.g. defined later with no path that
  runs it first) → error/warning so the author fixes it before running.

**Bundled fix — WORKFLOW-level `dependsOn` cycle detection (pre-existing gap, small change, same area):**
The pre-existing WORKFLOW-level `dependsOn` is otherwise correct — unlike step `dependsOn` it *does* TRIGGER
the referenced workflows (spec §5.8.4.1 has no "does not trigger" clause; a workflow is a separate entry
point that must be run to complete), collects their outputs into `$dependencies.<wfId>.*`, and fails clearly
on a failed/unknown dependency. BUT `executeDependencies` (`runner.go`) has **no visited/in-progress guard**,
so circular workflow deps (A dependsOn B, B dependsOn A) **infinite-recurse → stack-overflow crash**. Add a
visited set threaded through `ExecuteWorkflow`/`executeDependencies` to detect the cycle and **error clearly**
instead. (Keep the trigger behavior.) Optional while here: warn instead of silently `continue`-skipping a
cross-document `$sourceDescriptions.<name>.<workflowId>` dependency (full cross-doc execution stays deferred).

**Visualization (SEPARATE task, not Phase-7 runtime):** because there is no reordering, the execution
highlight stays sequential (unchanged). Add a **distinct `dependsOn` edge** to the graph and an **error/blocked
state** on a step whose gate fails. Tracked separately (belongs with the Phase-8 visualization work).

**Not in Phase 7:** async wait-with-timeout implementation (lands with Phases 8–11); cross-document
`dependsOn` execution (end-of-project Arazzo-source batch); any reordering/topological scheduling (explicitly
rejected as non-spec).

Tests: existing OpenAPI examples run unchanged; a step whose local `dependsOn` prerequisite completed runs
fine; a step whose `dependsOn` prerequisite did not run → workflow fails with a clear message; a failed
prerequisite does not satisfy the gate; LSP flags a cycle and a missing `stepId`; a **circular WORKFLOW-level
`dependsOn`** errors clearly instead of crashing (stack overflow).

</details>

### Phase 8: AsyncAPI Model Resolution — 🟡 IN PROGRESS (Parts 1 & 2 done; Part 3 remaining)

Goal: understand AsyncAPI sources (resolve + navigate + surface info) before real broker execution.
**NO visual/UI changes to the graph in this phase** — node appearance, badges, icons, and edges stay
exactly as they are (UI changes need team confirmation; they are parked in the final UI phase below).

**Status by part:**
- **Part 1 — CLI resolver ✅ DONE.** [asyncapi_finder.go](arazzo-designer-cli/internal/runner/executor/asyncapi_finder.go) (+ `asyncapi_finder_test.go`): `FindChannelByPath` (`source#/channels/x` via JSON Pointer), `FindOperationByID` (bare + scoped `$sourceDescriptions.<name>.<op>`, follows the operation's channel `$ref`), `ActionMismatch` detection. It **resolves/identifies** async targets only — it is **not** wired into step execution (no `Send`/`Receive`); that is Phase 9 (marked with a `TODO(phase9)` in the file).
- **Part 2 — LSP indexing + navigation + validation ✅ DONE.**
  - Indexing: [parser.go](arazzo-designer-lsp/navigation/parser.go) / [types.go](arazzo-designer-lsp/navigation/types.go) index AsyncAPI channels + operations (`extractChannels`, `ChannelInfo`, `AddChannel`/`LookupChannel`).
  - Navigation: [definition.go](arazzo-designer-lsp/server/definition.go) + [position_utils.go](arazzo-designer-lsp/server/position_utils.go) — go-to-definition for `channelPath` (`extractChannelKeyAtPosition` → channel location) and AsyncAPI operations; OpenAPI op nav unchanged.
  - Validation: [validator.go](arazzo-designer-lsp/validator/validator.go) — **`channelPath` present but `action` absent → ERROR** (direction undefined), plus channelPath format + source-type (`asyncapi`) checks. The **`operationId`/`action` mismatch** LSP diagnostic is deferred (needs cross-source resolution in the validator; enforcement rides with Phase 9 — prefer the AsyncAPI document's action and warn).
- **Part 3 — Visualizer properties panel ❌ REMAINING.** [NodePropertiesPanel.tsx](arazzo-designer-visualizer/src/views/WorkflowView/NodePropertiesPanel.tsx) currently renders General / Operation Details / Parameters / Request Body / Success Criteria / On Success / On Failure / Outputs — but **none** of `channelPath` / `action` / `correlationId` / `timeout` / `dependsOn` (the visualizer `src` has zero references to those fields). TODO: add an async-fields section to the panel — **properties panel ONLY**, async steps otherwise render as NORMAL steps (no distinct send/receive styling, no badges, no new edges). First check whether those fields already reach the panel's `nodeData` (it spreads `...stepData`) or need plumbing from the parsed step into the node model.

Tests: AsyncAPI source loads ✅; op/channel indexed ✅; `channelPath` navigation resolves ✅; channelPath-without-action errors ✅; **async metadata shown in the properties panel ❌ (Part 3)**; graph rendering otherwise unchanged.

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
- **`channelPath` requires `action`** → without it the runner can't choose send vs receive → hard error.
- **`operationId`/`action` mismatch** → the AsyncAPI document's operation action WINS; log a warning
  about the contradiction (spec doesn't define a conflict rule, so we don't hard-fail on this).

Blocking model & `dependsOn` (design decision to finalize here):
- The spec frames `dependsOn` around *"non-blocking/asynchronous"* steps (§5.8.5). Two viable models:
  (a) **blocking receive** — the receive step waits (up to `timeout`) inline; `dependsOn` stays a pure
  gate (Phase 7). Simpler. (b) **non-blocking receive** — the step starts listening in the background,
  the workflow proceeds, and a later step's `dependsOn` is what *waits* for completion. Pick (a) first
  unless a real use-case needs (b).
- When a receive step (or a `dependsOn` on a not-yet-complete async step) is waited on: wait up to
  `timeout`; **received in time → step completes (success);** **timed out → step fails.** This is the
  `dependsOn` "started-but-not-completed → wait-with-timeout" branch deferred from Phase 7 — it lands here.
- Execution-status visualization (existing node red/green driven by run telemetry) then reflects it:
  a completed async step shows success, a timed-out one shows failure. (No new node *styling* — that's
  Phase 13; this is just the existing pass/fail status coloring.)

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

### Phase 13 (FINAL): Visualizer UI Enhancements — ❌ NOT STARTED (⚠️ needs TEAM CONFIRMATION first)

Goal: the graph-appearance changes deliberately pulled OUT of Phase 8. Do these LAST, and only after
the UI direction is confirmed with the team — until then, async steps render as normal steps.

Changes (all visual):
- Source-type badges (OpenAPI / AsyncAPI / Arazzo) and `$self` in the overview.
- Render `send`/`receive` steps distinctly (icons/styling direction TBD with the team).
- Draw **step-level** `dependsOn` edges (workflow-level `dependsOn` edges: also confirm — none exist
  today). Must not break existing success/failure/goto edges. Note: no reordering exists at runtime
  (Phase 7 gate), so the execution highlight stays sequential and needs no change; a step blocked by
  its `dependsOn` gate could show an error/blocked state.

Tests: dependency edges render without breaking success/failure/goto edges; old workflows render
unchanged; badges/styling match the confirmed design.

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
(adapter) before 10–11. Phase 12 closes out docs/samples; Phase 13 (visualizer UI, needs team
confirmation) is the very last.

## Known Issues / Bugs (separate from the v1.1.0 phases — fix independently)

> **End-of-project cleanup batch.** None of these are v1.1.0 phase work. Best tackled together at the
> very end, after Phases 1–12, in one final pass: (1) the final XPath push (XPath selectors + `targetSelectorType: xpath`, see Phases 4/6), (2) the server-stop UI bug below, and (3) executable `type: arazzo` source descriptions below.

### BUG: stopping the Arazzo server doesn't reset the "server running" UI state
**Not related to v1.1.0** — a pre-existing extension lifecycle bug; tracked here so it isn't lost.

**Symptoms:**
- Starting a server correctly shows the red **stop** icon + the yellow "Arazzo server: stop" status-bar
  item; while running, the *Try with curl* / *Try with AI* CodeLenses appear and the webview buttons work.
- Clicking stop shows "Arazzo server stopped." and the pseudo-terminal closes (correct), **but the UI
  does not return to the not-running state**: the stop icons remain (the ▶ play button should be the
  only control), the *Try with curl* CodeLenses are still shown, and *Try with curl / Try with AI* in
  the webview no longer prompt "server not running — start it" (they behave as if it's still running).
- For a file whose server was **never started**, all of the above is correct (no stop icon, no
  CodeLenses, webview prompts to start) — so the problem is specifically that **stop doesn't
  propagate the state change** that the never-started case has.

**How it's wired (so the fix is targeted):** a single `notifyStateChange()`
([mcpServerRunner.ts](arazzo-designer-extension/src/mcp/mcpServerRunner.ts) line ~66) drives the refresh — it fires the callback registered via
`onMCPServerStateChange`, which in [extension.ts](arazzo-designer-extension/src/mcp/../extension.ts) (~line 146) does
`setContext('arazzoServerRunning', <running>)`. That context key gates the play/stop status-bar items
and the CodeLens `when` clauses, and the Run-workflow CodeLens provider also refreshes on it.
- **Start** path calls `notifyStateChange()` directly (line ~270) → UI shows "running".
- **Stop** (`stopMCPServer`, ~line 312) deliberately does *not* call it; it relies on the
  **task-end listener** registered in `initializeMCPServerRunner` (~line 340) which, on `onDidEndTask`,
  clears state and calls `notifyStateChange()`.

**Suspected root cause(s) to investigate:**
1. The task-end listener (`registerMCPTaskEndListener`) may not fire (or not match) when the task is
   **terminated** via stop (vs. exiting on its own), so `notifyStateChange()` never runs on stop.
2. Or it fires too early — while `isMCPServerRunning()` (`isMCPTaskRunning()`) still reports `true` —
   so the `setContext('arazzoServerRunning', …)` callback recomputes "running = true" and nothing resets.
3. The **webview's** running state (Try with curl/AI prompt) may be cached/driven separately from the
   `arazzoServerRunning` context key, so even when the others reset, the webview isn't told the server stopped.

**Fix direction:** ensure stop reliably flips the state to not-running — e.g. have `stopMCPServer`
itself call `notifyStateChange()` after stopping (so it doesn't depend solely on the task-end event),
make the `arazzoServerRunning` callback read the post-stop state, and ensure the webview is notified
(or reads `isMCPServerRunning()` live) so its Try-with-curl/AI prompt resets. Then verify all three
reset on stop: status-bar play/stop toggle, CodeLenses, and the webview prompt.

**Files:** `arazzo-designer-extension/src/mcp/mcpServerRunner.ts` (`stopMCPServer`, `notifyStateChange`,
`initializeMCPServerRunner`/task-end listener), `arazzo-designer-extension/src/extension.ts`
(`arazzoServerRunning` context callback, status-bar items), `mcp/runWorkflowCodeLens.ts`,
`mcp/mcpPlaygroundWebview.ts` (webview running state).

### GAP: `type: arazzo` source descriptions (external Arazzo documents) are not executable
**Pre-existing since v1.0.1 — NOT a v1.1.0 item.** Recorded here so it isn't lost; tackle at the very
end alongside the final XPath push and the server-stop bug (do not weave it into the phases).

**Current behavior:**
- `loader.LoadSourceDescriptions` loads/parses *any* source (including `type: arazzo`) into the sources
  map, and Phase 5 can now READ `$sourceDescriptions.<name>.<workflowId>` as a *value* (returns the
  external workflow object — see `findWorkflowIDInSpec`).
- BUT the runner only ever **executes local** workflows: `executeDependencies` explicitly skips
  `$sourceDescriptions.*` deps (`runner.go` ~line 476), and step `workflowId` / `goto` / `dependsOn`
  in the cross-document form are never actually invoked.

**Missing:** invoking a workflow defined in an *external* Arazzo source — i.e. a step `workflowId`
(or `operationId`) / `goto` / `dependsOn` pointing at
`$sourceDescriptions.<name>.<workflowId>[.steps.<stepId>]` should load that external document's
workflow and execute it, threading inputs/outputs across documents. This also covers the deep
§5.5.2 external-Arazzo-document identity matching already flagged as deferred in Phase 3.

**Scope when done:** resolve external workflow references; execute them via a sub-runner over the
loaded external Arazzo doc; map inputs → external workflow inputs and collect its outputs back;
guard against cycles across documents. (The LSP already validates the external `dependsOn` form, so
that side needs no change.)
