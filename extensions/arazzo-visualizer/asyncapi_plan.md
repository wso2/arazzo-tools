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

**Why "three model layers" matters.** The same Arazzo document is parsed independently by three
codebases that cannot import each other: the **TS visualizer** (`src/types`), the **Go LSP**
(`arazzo-designer-lsp/parser`), and the **Go CLI runner** (`internal/models`). A field added to one
and missed in another does not fail loudly — it is silently dropped, so a step renders in the graph
but does not execute, or validates in the editor but is ignored at runtime. Keeping the three
structurally aligned is therefore a standing invariant of this project, not a one-off task.

**Already implemented** (see audit above): all three model layers accept `1.1.0`, carry
`$self`, `asyncapi`, `channelPath`, `timeout`, `correlationId`, `action`, step `dependsOn`,
`querystring`, action `parameters`, `ExpressionTypeObject`, `SelectorObject`,
`PayloadReplacementObject.targetSelectorType`, and widened `outputs`/`value` types.

**Rules established here (enforced by the LSP, see Phase 2):**

| rule | outcome |
|---|---|
| `arazzo` version not one of `1.0.0`, `1.0.1`, `1.1.0` | error — *Invalid arazzo version* |
| a step declares **none** of `operationId` / `operationPath` / `channelPath` / `workflowId` | error — *Must have one of …* |
| a step declares **more than one** of those four | error — *Can only have one of …* |
| `sourceDescriptions[].type` not `openapi` / `asyncapi` / `arazzo` | error |
| a v1.0.x document | parses and runs **exactly as before** — v1.1.0 support is additive |

**The one intentional divergence, still live.** The CLI models Selector Objects and Expression Type
Objects as `interface{}` rather than typed structs. That is fine for parsing and round-tripping, but
it means the CLI cannot type-check them — which is why Phase 4 had to add a *decode helper*
(`IsSelectorObject` / `ResolveExpressionType`) instead of relying on the model, and why the LSP
validates Expression Type Objects only where they appear in a typed position (criterion `type`), not
inside untyped maps like outputs and payloads. Documented rather than fixed, deliberately: typing them
in the CLI would ripple through every `map[string]interface{}` the executor passes around.

**Limits:**
- Version acceptance is an exact string match against three known values, not a semver range — a
  future `1.1.1` would be rejected until added explicitly.
- Nothing validates the *referenced* OpenAPI/AsyncAPI documents against their own schemas; the tooling
  only reads the parts it needs (channels, operations, message content types).

Verification tasks (no new modeling expected):
- Confirm the three model layers stay structurally aligned (TS <-> LSP <-> CLI).
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

**The severity policy** ([validator.go](arazzo-designer-lsp/validator/validator.go) — 66 diagnostics
as of Phase 10). It is applied consistently and is worth stating because it decides how every later
phase reports a problem:
- **error** — the document violates the spec, or names something that does not exist. It cannot run
  correctly as written.
- **warning** — legal per the spec, but very likely not what the author meant, or a place where the
  runtime will silently pick something for them (an unfiltered receive, a step value overriding the
  AsyncAPI declaration, a reference to an unknown *source description* — which may simply be
  unresolvable from the editor).
- **information** (added in Phase 10) — nothing is wrong; the runtime will apply a default that
  neither document states, and the author may want to state it.

A check that depends on a fact **outside the Arazzo text** (an operation's `action`, a channel's
declared content type) must stay **quiet when it cannot resolve that fact** rather than guess — see
the injected-resolver pattern in Phases 9 and 10.

**Diagnostic inventory by area** (message text lives in the validator; this is the map):

| area | checks |
|---|---|
| document | `arazzo` present + version valid; `info.title`; `info.version`; at least one `sourceDescriptions`; at least one `workflows` |
| sourceDescriptions | `name` and `url` required; `type` enum; `$self` must not contain a fragment (spec §5.8.1.1) |
| workflows | `workflowId` required + unique; at least one step |
| steps | `stepId` required + unique; exactly one target field; `action` enum + AsyncAPI-only; `timeout` non-negative; `correlationId` only on AsyncAPI receives; non-empty `successCriteria` when present |
| targeting | `channelPath` / `operationPath` format, unknown source, source-type rules; scoped `operationId` expression form |
| `dependsOn` | reference forms (3), existence, self-reference, step-level cycles, workflow-level cycles |
| components | key charset `[a-zA-Z0-9.\-_]+`; `$components.<section>.<key>` reference resolution |
| actions | `type` required + enum; `stepId`/`workflowId` mutually exclusive; action `parameters` only with `workflowId`; `in` forbidden on action parameters |
| expressions | Expression Type Object `type` enum + required/valid `version`; parameter `in` enum |
| references | forward-reference warning (a step referencing one declared after it) |

**Limits:**
- `validateRuntimeExpressions` only special-cases `$steps` / `$workflows` roots; there is no default
  branch, so an unknown or misspelled root is **not** flagged. This is deliberate — it is what let the
  Phase-5 roots land without the LSP rejecting them — but it means `$stpes.foo` passes validation.
- Two known blind spots remain, tracked under Known Issues below ("two remaining LSP validation blind
  spots"). Both are missed detections, not false alarms.

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

**Base-URI determination** (spec §5.5, RFC3986 §5.1.1–5.1.4). `retrievalPath` is where the Arazzo
document itself was loaded from:

| `$self` | base URI becomes | effect |
|---|---|---|
| absent | the retrieval URI (the document's own directory) | **v1.0.x behaviour, unchanged** |
| absolute (`https://ex.com/flows/a.yaml`) | `$self` itself | relative sources become **remote URLs and are fetched** |
| relative (`./flows/a.yaml`) | `$self` resolved against the retrieval URI | the resolved absolute URI is the base |

**Source-location resolution:** a `sourceDescriptions.url` that is itself absolute — remote or an
absolute local path — **always wins over the base URI** and is used as-is. Only relative URLs are
resolved against the base.

**Dedup is by resolved location, not by declared name.** Two source descriptions with different names
that resolve to the same file are loaded **once**. That is the shallow half of the spec's
identity-over-location rule; the deep half is deferred (below).

**Why only the CLI changed.** The other two layers need no change and it is worth recording why, so a
future reader does not "fix" them: the **LSP** parses the whole document before use and (at this
point) discovered OpenAPI files by directory scan, never resolving `sourceDescriptions.url` at all —
*this was later revisited in Phase 8*, which replaced the scan with `$self`-aware resolution of the
declared sources so navigation resolves exactly as execution does. The **RPC client** does no URL
resolution. Spec §5.5.1 (full parse before resolution) already held in the CLI, since `LoadArazzoDoc`
parses everything before `LoadSourceDescriptions` runs.

**Limits / deferred:**
- The **deep form of §5.5.2 identity matching** — loading an *external Arazzo document* and matching a
  cross-document workflow reference against the *target's* declared `$self` — is not wired up, because
  the runner does not execute workflows in external Arazzo documents at all. That belongs with
  cross-document workflow execution (it overlaps Phase 7's cross-document `dependsOn`) and is tracked
  in the end-cleanup batch under Known Issues.
- Remote sources are fetched with no caching, auth, or retry.
- The LSP keeps a **standalone copy** of this resolution logic ([resolve.go](arazzo-designer-lsp/server/resolve.go),
  added in Phase 8) because the two Go modules cannot import each other. The two copies must be kept
  in step by hand — the same constraint that later forced `utils.SameMediaType` to be duplicated in
  Phase 10.

Original spec-derived requirements (for reference):
- Enforce full-document parse before any reference resolution (spec §5.5.1) in the LSP loader,
  CLI loader, and RPC client.
- Establish the base URI per RFC3986 §5.1.1–5.1.4 priority (see the table above).
- Resolve relative `sourceDescriptions.url` against that base URI.
- Identity-based matching for external Arazzo refs: if the target has `$self`, the reference
  MUST match the `$self` URI, not just the retrieval location (spec §5.5.2). See spec
  Appendix B ("Examples of Base URI Determination and Reference Resolution") for worked cases.
- Preserve current local-relative behavior for v1.0.x (no `$self` → file path is base URI).

Tests: relative source with `$self` absent; with absolute `$self`; relative `$self` resolved
against retrieval URI; remote-style `$self` + relative source URL; two docs sharing a `$self`
treated as one; full parse before resolution; existing examples still load.

### Phase 4: Selector Objects And Expression Types — ✅ DONE except XPath (2026-06-17)

Goal: support the new structured-extraction model. **Before this phase Selector Objects were preserved
verbatim and never evaluated.**

**Implemented.** New shared selector-evaluation service [internal/evaluator/selector.go](../../arazzo-designer-cli/internal/evaluator/selector.go):
`IsSelectorObject` (detects a `{context, selector, type}` map), `EvaluateSelectorObject`
(resolves the `context` expression, then routes by dialect), `resolveExpressionType` (handles
bare-string or Expression Type Object `type`, applies default versions, rejects unknown
dialects/versions), and `EvaluateJSONPathValue` (value-returning JSONPath, complementing the
existing bool criterion engine). `jsonpointer` reuses `ResolveJSONPointer` (RFC 6901); `jsonpath`
reuses the `ojg` engine (RFC 9535). **XPath returns a clear "not yet supported" error.**

**What a Selector Object is, and how it is recognised.** A three-key map — `context` (a runtime
expression naming what to select *from*), `selector` (the query), and optional `type` (the dialect).
Detection is structural (`IsSelectorObject`), not schema-driven, because the CLI models these as
`interface{}` (the Phase-1 divergence). Evaluation is always two steps: resolve `context` through the
normal expression evaluator, then run `selector` against the result in the chosen dialect.

**Dialect / version matrix** (spec §5.8.12). The **defaults are *tooling* defaults, applied only when
the Expression Type Object is absent entirely** — the object itself always requires BOTH `type` and
`version`, and omitting `version` inside it is an error rather than a fallback:

| `type` | allowed `version` values | tooling default when the object is absent |
|---|---|---|
| `jsonpath` | `rfc9535`, `draft-goessner-dispatch-jsonpath-00` | `rfc9535` |
| `xpath` | `xpath-31`, `xpath-30`, `xpath-20`, `xpath-10` | `xpath-31` |
| `jsonpointer` | `rfc6901` | `rfc6901` |

Unknown dialect, or a version not in the row, is a clear error at both layers — never a silent
fallback to a default engine.

**Where Selector Objects are honoured** — every position v1.1.0 permits, all routed through one
service so they cannot diverge:

| position | wired in |
|---|---|
| step `outputs` | [output_extractor.go](../../arazzo-designer-cli/internal/runner/executor/output_extractor.go) |
| workflow `outputs` | [runner.go](../../arazzo-designer-cli/internal/runner/runner.go) `resolveWorkflowOutputs` |
| parameter `value` | [parameter_processor.go](../../arazzo-designer-cli/internal/runner/executor/parameter_processor.go) |
| `requestBody.payload` values (nested) | same, via the central `processValue` recursion |
| payload replacement `value` | same (this is Phase 6's value side) |

**Rules — what fails, what is skipped, what is silent:**

| condition | outcome |
|---|---|
| unknown `type`, or a `version` not allowed for it | **error** (runtime), **error** diagnostic on a criterion `type` (LSP) |
| Expression Type Object present but `version` omitted | **error** — the object requires both fields |
| `type: xpath` anywhere | **error**: not yet supported |
| a selector evaluates but resolves to **nil** in an output | **warning**, and the output is **not extracted** (rather than emitting a null) |
| a selector fails in a parameter / payload / replacement | **warning**, and the value is **left unreplaced** rather than injecting garbage |
| a plain string runtime expression | untouched — this phase adds a path, it does not change the existing one |

**Limits:**
- **XPath is unimplemented** and errors. The same missing XML/XPath engine also blocks Phase 6's
  `xpath` replacement targets, so the two are deliberately deferred as **one final XPath push**
  together with `targetSelectorType`'s XML default (see the end-cleanup batch under Known Issues).
- The **LSP validates Expression Type Objects only on criterion `type`**, a typed position. Inside
  outputs, payloads and `targetSelectorType` they live in untyped maps, so they are runtime-validated
  only — an author gets the error on run, not while typing. Tightening this is optional follow-up.
- Selector evaluation resolves `context` through the evaluator, so a Selector Object is only as
  reachable as its context expression: `$message.payload` selectors do nothing until an async runtime
  populates `$message` (Phase 9).

Tests: `evaluator/selector_test.go`, `executor/output_extractor_test.go`,
`executor/parameter_processor_test.go`, and an LSP `TestExpressionType` — all green; full CLI + LSP
suites green. Examples: `phase4_selectors/`.

### Phase 5: Runtime Expression Upgrade — ✅ DONE (branch `asyncV1-phase5`)

Goal: bring the evaluator to v1.1.0. **Previously the evaluator lacked
`$message`/`$self`/`$sourceDescriptions`(general)/`$components` and compound boolean criteria.**

Implemented in `internal/evaluator/evaluator.go` (+ `internal/models/models.go`, `internal/runner/runner.go`).

**Expression roots, complete** (spec §5.9) — the pre-existing ones are listed too, because "what
resolves" is the reference an author actually needs:

| root | resolves from | added here |
|---|---|---|
| `$statusCode`, `$response`, `$response.header.*`, `$response.body[#/…]` | the HTTP response | no |
| `$inputs`, `$inputs.*` | workflow inputs | no |
| `$steps.<id>.*` | recorded step data | no |
| `$dependencies.<wfId>.*` | outputs of a workflow-level `dependsOn` | no |
| `$self` | the document's `$self` field | **yes** |
| `$message`, `$message.header.*`, `$message.payload[#/…]` | the async evaluation context — **nil until Phase 9 populates it** | **yes** |
| `$components.<type>.<name>` | the document's `components` | **yes** |
| `$workflows.<id>.<field>` | the document's workflows | **yes** |
| `$url`, `$method` | the execution context | **yes** |
| `$sourceDescriptions.<name>.<ref>` | see the two-step rule below | **yes** |

**`$sourceDescriptions.<name>.<ref>` — the §5.9.2 two-step priority.** `<ref>` is matched **first**
against an `operationId` (OpenAPI/AsyncAPI) or `workflowId` (Arazzo) *inside the referenced document*;
**only when there is no match** is it treated as a field of the Source Description Object itself
(`url`, `type`, …). The order is implemented explicitly so resolution can never be ambiguous. Source
kind comes from the SD Object's declared `type`, falling back to the spec's marker key.

> Not to be confused with the **operation-targeting** forms in a step's `operationId` /
> `operationPath`, which `operation_finder.go` already handled. This root is the *general expression*
> form usable anywhere an expression is allowed.

**Compound boolean criteria.** `EvaluateSimpleCondition` became a quote-aware recursive-descent
evaluator. The signature is unchanged, so every existing caller was untouched.

| supported | detail |
|---|---|
| logical | `!`, `&&`, `\|\|`, parentheses (with precedence) |
| comparison | `==`, `!=`, `>`, `<`, `>=`, `<=` |
| access | property dereference, array indexing |
| operands | run through the **full** expression evaluator, so any root above works inside a condition |
| malformed input | **warning** — *malformed condition … (unparsed input or unbalanced parentheses); treating as false* — the condition evaluates **false** rather than erroring the step |

**Embedded `{$…}` serialization** (`resolveTemplateString` / `embedValue`):

| value | embedded as |
|---|---|
| string | as-is |
| object / array | **JSON** — not Go's `map[k:v]` rendering |
| other primitives | default formatting |
| unresolved (nil) | the **placeholder is left in place**, with a context-aware warning naming the expression and the template |

Leaving an unresolved placeholder rather than substituting an empty string is deliberate: an empty
substitution produces a plausible-looking but wrong request, while a surviving `{$inputs.missing}` is
visibly broken in the output and in the log.

**State threading:** `ExecutionState` gained `Self`, `Components`, `SourceDescriptionObjects` and
`WorkflowsByID`, populated by the runner from the Arazzo document.

**LSP:** no change was needed — `validateRuntimeExpressions` only special-cases `steps`/`workflows`
and has no default branch, so the new roots are not flagged. The flip side is recorded under Phase 2's
limits: an unknown root is not flagged either.

**Limits / deferred:**
- **Case-insensitive string comparison is not implemented** — comparisons are case-sensitive. No clear
  spec requirement was located mandating otherwise; revisit if one is found for a specific operator.
- A malformed condition degrades to `false` rather than failing the step. That keeps a typo from
  aborting a run, but it means a criterion can silently never match — the warning is the only signal.
- `$message` resolves to nil outside an async step, so a criterion referencing it in a REST step is
  quietly false rather than an error.

**Tests:** `internal/evaluator/evaluator_phase5_test.go` (all roots, §5.9.2 priority, compound/grouped
conditions, embedded JSON). Build + vet + full suites green on both modules.

### Phase 6: Payload Replacement Upgrade — ✅ DONE except XPath (deferred to the end-of-project XPath push)

Goal: bring Payload Replacement Objects to v1.1.0 on **both** sides — the value being written and the
target it is written to.

Status:
- **Value side — ✅ done (Phase 4):** a replacement `value` can be a literal, a runtime expression,
  or a Selector Object; `applyReplacements` evaluates it through the shared service.
- **Target side — ✅ done for JSON Pointer + JSONPath:** `applyReplacements` reads `targetSelectorType`
  (a bare string or an Expression Type Object, via `evaluator.ResolveExpressionType`) and routes the
  `target` accordingly.
- **Target side — ❌ XPath only:** an `xpath` `targetSelectorType` logs a clear "not yet supported"
  warning.

**Target dialects:**

| `targetSelectorType` | engine | notes |
|---|---|---|
| omitted | **JSON Pointer** (`setJSONPointer`) | the default for JSON payloads |
| `jsonpointer` | `setJSONPointer` | **array indices supported** (`/items/0/product_id`), mirroring the read side |
| `jsonpath` | `evaluator.SetJSONPath` (ojg `Set`) | |
| `xpath` | — | warning, replacement skipped; blocked on the same missing engine as Phase 4 |

**Rules — every failure mode skips the replacement rather than corrupting the payload.** This is the
governing design decision of the phase: a replacement that cannot be applied leaves the payload as it
was, and says so. It never writes a partial path, a null, or a stringified error.

| condition | outcome (all warnings; the payload is left untouched) |
|---|---|
| `targetSelectorType` invalid | *replacement 'targetSelectorType' invalid: …* |
| JSON Pointer target not starting with `/` | *JSON Pointer replacement target … must start with '/'* |
| JSONPath target fails to apply | *JSONPath replacement target failed: …* |
| `xpath` target | *XPath replacement targets are not yet supported* |
| pointer names a missing object key | *… not applied: missing object key …* |
| pointer names an out-of-range or non-numeric array index | *… not applied: invalid array index … (array length N)* |
| pointer descends into a scalar | *… not applied: cannot descend into segment … (node is neither object nor array)* |
| the replacement **value** resolves to nil (literal, expression, or selector) | *… resolved to nil …; skipping replacement* |

**Limits / remaining for the XPath follow-up:**
- Add the XML/XPath engine and route `xpath` replacement targets (and Phase 4's `xpath` selectors) to it.
- Apply the **XML default**: when `targetSelectorType` is omitted and the payload is XML, the target
  should be treated as XPath. Only the JSON default (JSON Pointer) is in place today, so an XML payload
  with an omitted type is currently interpreted as a JSON Pointer.
- Optional: LSP validation of `targetSelectorType` as an Expression Type Object (version required +
  valid per type) — today it is runtime-validated only, since it sits in an untyped map.
- Because every failure is a **warning, not an error**, a workflow with a mistyped target still
  reports success. The run log is the only place the skip is visible.

Tests: `evaluator.TestSetJSONPath`, `executor.TestApplyReplacements_JSONPathTarget`,
`executor.TestApplyReplacements_JSONPointerArrayIndex`; examples
`phase4_selectors/07-jsonpath-replacement-target` and `08-jsonpointer-array-target`.

### Phase 7: Step Dependencies (`dependsOn`) — ✅ DONE

**The governing design decision (spec §5.8.5.1): `dependsOn` is a completion GATE, not a reordering
directive and not a trigger.** The spec says *"A list of steps that MUST be completed before this step
can be executed. `dependsOn` only establishes a prerequisite relationship … and does not trigger
execution of the referenced steps."* So the runner keeps executing in the existing order (document
order plus `goto`/`onSuccess`/`onFailure`/`retry`, all unchanged) and only adds a **check before each
step**. Topological scheduling was explicitly rejected as non-spec.

**"Completed" means the prerequisite step RAN and reached terminal SUCCESS.** A prerequisite that ran
and failed does not satisfy the gate; neither does one that was skipped.

**Implemented:**
- **Runtime step gate** ([runner.go](../../arazzo-designer-cli/internal/runner/runner.go) `checkStepDependencies`) — checked before each step; no reordering, no triggering; hard error on an unmet prerequisite.
- **Cross-workflow step granularity** — the gate verifies the **specific** referenced step reached success, not merely that the workflow ran. The runner surfaces each dependency workflow's per-step statuses (`WorkflowExecutionResult.StepsStatus` → `ExecutionState.DependencyStepStatus`), so a step skipped via `goto` inside a dependency correctly fails the gate.
- **Workflow-level `dependsOn` cycle guard** ([runner.go](../../arazzo-designer-cli/internal/runner/runner.go) `executeDependencies` `depStack`) — a circular workflow dep now errors clearly instead of stack-overflowing (trigger behavior kept).
- **LSP static validation** ([validator.go](arazzo-designer-lsp/validator/validator.go)) — `dependsOn` reference forms + existence + self-reference, plus **cycle detection** for both **step-level** (`validateDependsOnCycles`) and **workflow-level** (`validateWorkflowDependsOnCycles`) `dependsOn`.

**Reference forms and what each does at runtime:**

| form | runtime behaviour |
|---|---|
| bare `stepId` (same workflow) | full gate — the step must be `StepStatusSuccess` |
| `$workflows.<wfId>.steps.<stepId>` | the workflow must have run **as a dependency** (its per-step statuses recorded) **and** that specific step must have succeeded |
| `$sourceDescriptions.<name>.<wfId>.steps.<stepId>` | **form-validated only** — hard error at runtime: *cross-document step dependencies are not yet supported* |

**Rules — every unmet gate is a HARD ERROR, never a wait and never a skip:**

| condition | outcome |
|---|---|
| local prerequisite did not reach success | error: *step 'X' dependsOn 'Y', which has not completed successfully* |
| referenced workflow never ran as a dependency | error: *… but workflow 'W' has not run as a dependency* |
| referenced cross-workflow step did not succeed | error: *… but step 'S' in workflow 'W' did not complete successfully* |
| malformed `$workflows.…` reference | error: *has a malformed dependsOn reference* |
| any `$sourceDescriptions.…` step reference | error: *cross-document step dependencies are not yet supported* |
| circular `dependsOn` (step level, or workflow level) | LSP **error** at authoring time; workflow-level cycles also guarded at runtime instead of crashing |

**Step-level vs workflow-level `dependsOn` behave DIFFERENTLY, on purpose.** Step `dependsOn` is a
pure gate (the spec's "does not trigger" clause). Workflow `dependsOn` (§5.8.4.1) has no such clause —
a workflow is a separate entry point that must be run to complete — so it **does trigger** the
referenced workflows, collects their outputs into `$dependencies.<wfId>.*`, and fails clearly on a
failed or unknown dependency. Both were kept as-is; only the missing cycle guard was added.

**Examples**: [examples/async_test/phase7/](../../examples/async_test/phase7) 01–08 (gate satisfied/unmet, workflow dep, step & workflow cycles, cross-workflow dep, cross-workflow specific-step-ran vs step-skipped-by-goto). Unit tests: `runner_phase7_test.go`, `validator_test.go`.

**Limits / deferred:**
- **The async wait branch is designed but not wired.** The intent is that if a prerequisite *started
  but has not reported completion*, an async step waits up to a timeout rather than failing
  immediately. Nothing uses it, because the blocking model chosen in Phase 9 means a receive completes
  inline — so a prerequisite is always either done or not. It becomes real only with **Phase 14**
  (non-blocking async steps).
- **Cross-document `dependsOn` execution** is a hard error, pending executable `type: arazzo` sources
  (end-cleanup batch, and it needs Phase 3's deferred §5.5.2 identity matching).
- The LSP's "dependency cannot have completed by document/flow order" check is only partly there — a
  forward reference produces a **warning** (*declared after this step … unless a 'goto' runs it
  first*), not a full reachability analysis.
- **Visualization of `dependsOn` edges and blocked-step state is not done** — it needs a distinct graph
  edge and a blocked state on the gated step. Parked in Phase 13 (needs team sign-off).

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

### Phase 8: AsyncAPI Model Resolution — ✅ DONE

Goal: understand AsyncAPI sources (resolve + navigate + surface info) before real broker execution.
**NO visual/UI changes to the graph in this phase** — node appearance, badges, icons, and edges stay
exactly as they are (UI changes need team confirmation; they are parked in the final UI phase below).

**The three targeting forms, and the exact pointer shapes** — this is the reference the rest of the
async phases build on. A step targets an operation or a channel in one of three ways:

| form | shape | resolves to |
|---|---|---|
| `operationId` (bare) | `publishEvent` | an operation of that id in any declared source |
| `operationId` (scoped) | `$sourceDescriptions.<name>.<operationId>` | that operation in that source only |
| `channelPath` | `<source>#/channels/<key>` | an AsyncAPI channel (direction comes from the step's `action`) |
| `operationPath` | `<source>#/operations/<id>` (AsyncAPI) or `<source>#/paths/~1products/get` (OpenAPI) | an operation by JSON Pointer — `~1` = `/`, `~0` = `~` |

**Both source-reference spellings are accepted everywhere.** The spec REQUIRES a runtime expression
(`{$sourceDescriptions.<name>.url}#…`) in `channelPath`/`operationPath`, while a bare source name is
the common shorthand. One shared helper ([utils/sourceref.go](arazzo-designer-lsp/utils/sourceref.go):
`NormalizeSourceRef`, `SplitSourceRefAndPointer`, `ParseScopedOperationID`, `SplitJSONPointer` incl.
`~1`/`~0` unescaping) is used by navigation, hover **and** validation, so the three cannot disagree.
**Fixed here:** the validator previously reported the spec-mandated expression form as an "unknown
source description".

**Status by part:**
- **Part 1 — CLI resolver ✅ DONE.** [asyncapi_finder.go](../../arazzo-designer-cli/internal/runner/executor/asyncapi_finder.go) (+ `asyncapi_finder_test.go`): `FindChannelByPath` (`source#/channels/x` via JSON Pointer), `FindOperationByID` (bare + scoped, follows the operation's channel `$ref`), `ActionMismatch` detection. It **resolves/identifies** async targets only — it is **not** wired into step execution (no `Send`/`Receive`); that is Phase 9. *(`FindOperationByPath` — the third form at runtime — was missing here and only added in Phase 10; see that phase's "Phase 9 gap closed here".)*
- **Part 2 — LSP indexing + navigation + validation ✅ DONE.**
  - Indexing: [parser.go](arazzo-designer-lsp/navigation/parser.go) / [types.go](arazzo-designer-lsp/navigation/types.go) index AsyncAPI channels + operations (`extractChannels`, `ChannelInfo`, `AddChannel`/`LookupChannel`).
  - Navigation: [definition.go](arazzo-designer-lsp/server/definition.go) + [position_utils.go](arazzo-designer-lsp/server/position_utils.go) — go-to-definition for all three forms. Hardened during testing:
    - **Scoped to the document's declared `sourceDescriptions`**, not a workspace/directory scan — a reference resolves only inside the specs this Arazzo doc declares, never into an unrelated same-named op elsewhere. Per-file lookups (`LookupOperationInFile`/`LookupChannelInFile`) bypass the global deduped map. This is the phase's second big design decision: **name resolution is document-scoped**, because a source name means nothing outside the document that declared it.
    - **On open/change, only the declared sources are parsed/indexed** ([server.go](arazzo-designer-lsp/server/server.go) `indexDeclaredSources`) — the old directory-scan path (`BuildIndex`/`DiscoverOpenAPIFiles`) is now dead (marked `// NOT USED`).
    - **`$self`-aware source resolution** ([resolve.go](arazzo-designer-lsp/server/resolve.go)) mirrors the runner (spec §5.5), so navigation resolves relative source URLs exactly as execution does (standalone copy — the two Go modules cannot import each other; see Phase 3's limits).
    - **Hover uses the same scoped resolver as Go-to-Definition** (`lookupOperationInSources`), so the popup cannot disagree with the click target. Hover also covers `channelPath` (channel key + broker address) and `operationPath`.
    - **`operationPath` navigation is new** — it had never existed in any version.
    - **Indexing runs on open, change AND save**, always file-scoped: saving an Arazzo file re-resolves and re-indexes its declared sources (so a newly added `sourceDescription` works without reopening); saving a source spec re-indexes that file (AsyncAPI files included, not just `openapi:`).
    - **Per-document typed source registry** ([server/source_registry.go](arazzo-designer-lsp/server/source_registry.go)) — for each Arazzo document it records every declared source's name, declared `type`, the type the file **actually** is (`OpenAPIFile.SpecType`), resolved file URI, and a remote flag; exposes `AsyncSources`/`RESTSources` so **event-driven sources are tracked separately from REST ones**, plus `TypeMismatch()` when a file contradicts its declared type. Entries are document-scoped and dropped on close. Surfaced to clients via the additive **`arazzo/getSourceInfo`** LSP method (`{sources, async, rest}`); `arazzo/getModel`'s shape is unchanged.
  - **Part 3 — Visualizer properties panel ✅ DONE.** [NodePropertiesPanel.tsx](arazzo-designer-visualizer/src/views/WorkflowView/NodePropertiesPanel.tsx) shows, on a clicked step: a **Step Type** field, an **AsyncAPI** section (`channelPath`/`action`/`correlationId`/`timeout`), and a **Depends On** section. **Properties panel ONLY** — no node/graph/badge/edge changes (those stay in Phase 13).

**Step Type resolution rules** (properties panel): `channelPath`/`action` → AsyncAPI; a **scoped**
`operationId` resolves to its source's declared type; a **bare** `operationId` resolves only when the
document declares exactly one typed source, otherwise it falls back to OpenAPI; `workflowId` →
Workflow.

**Diagnostics added here:**

| condition | severity |
|---|---|
| `channelPath` present but `action` absent | **error** — direction is otherwise undefined |
| `channelPath` / `operationPath` not `<source>#<pointer>` | **error** |
| `channelPath` / `operationPath` / `operationId` names an unknown source description | **warning** — it may simply be unresolvable from the editor |
| `channelPath` source is not `type: asyncapi` | **error** |
| `operationPath` source is `type: arazzo` | **error** — use `workflowId` |
| scoped `operationId` not `$sourceDescriptions.<name>.<operationId>` | **error** |

The **`operationId`/`action` mismatch** diagnostic was originally deferred out of this phase because it
needs cross-source resolution the validator could not do — it lands in Phase 9 via the injected
step-action resolver.

Tests: AsyncAPI source loads ✅; op/channel indexed ✅; **all three targeting forms** navigate to the right file+line in **both source-reference spellings** ✅; hover matches the click target for every form ✅; navigation stays scoped to declared sources ✅; `$self`-aware resolution matches the runner ✅; per-document registry records declared/resolved types, splits async vs REST, flags type mismatches, and is dropped on close ✅; save re-indexes declared sources ✅; channelPath-without-action errors ✅; targeting validation ✅; async metadata + Step Type shown in the properties panel ✅; graph rendering otherwise unchanged ✅. Examples: [examples/async_test/phase8/](../../examples/async_test/phase8) (01 panel/nav, 02 operationId, 03 async validation, **04 every targeting form × both spellings, 05 targeting validation**) — 04 and 05 are also test fixtures, so the shipped examples are verified to behave exactly as their headers claim.

**Limits / deferred:**
- **A whole `channelPath` value is not one clickable link** — it needs a DocumentLink provider.
  Ctrl+click and hover already navigate correctly; the link is just segmented.
- **The dead directory-scan code is still present** (`BuildIndex`/`DiscoverOpenAPIFiles`, marked
  `// NOT USED`) — removal is in the end-cleanup batch.
- A **bare** `operationId` in a document with several typed sources cannot be resolved to a type with
  certainty, so the panel falls back to OpenAPI. Scoping the id removes the ambiguity.
- The registry records a `TypeMismatch()` but nothing surfaces it as a diagnostic yet.

### Phase 9: AsyncAPI Adapter Runtime — ✅ DONE (blocking model; in-memory adapter)

Goal: make AsyncAPI steps actually EXECUTE. Mostly CLI runner work, but the last three bullets also
touch the LSP (a validator hook) and the visualizer (rendering the new message spans) — an async step
should behave, be diagnosed, and be inspected exactly like a REST step.

**The design decision that shapes everything after it: the BLOCKING model (option (a)).** The spec
frames `dependsOn` around *non-blocking/asynchronous* steps, which allows two models — (a) a receive
that waits inline up to `timeout`, with `dependsOn` staying the pure Phase-7 gate, or (b) a receive
that starts listening in the background while the workflow proceeds, with a later step's `dependsOn`
doing the waiting. **(a) was chosen** as the simpler model that makes async steps behave like every
other step. (b) is deferred to Phase 14 — and until then, Phase 7's "started but not completed → wait
with timeout" branch has nothing to wait for.

**Implemented:**
- **Adapter interface + in-memory adapter** — [adapter.go](../../arazzo-designer-cli/internal/runner/executor/adapter.go) (`Adapter` = `Send`/`Receive`/`Name`, normalized `Message`) and [adapter_inmemory.go](../../arazzo-designer-cli/internal/runner/executor/adapter_inmemory.go) (broker-less FIFO queues + timeout + a simple correlation heuristic). Default adapter is in-memory; a nil adapter yields the clear "requires a configured adapter" error. Real brokers = Phase 11.
- **Send/receive wiring** — [async_executor.go](../../arazzo-designer-cli/internal/runner/executor/async_executor.go): `resolveAsyncTarget` routes a step to the async path when it has a `channelPath` or an `operationId` that resolves to an AsyncAPI operation (OpenAPI ops stay on the HTTP path). `send` builds payload/headers (reusing `ParameterProcessor`), serializes (basic JSON at this point) and `Send`s; `receive` evaluates `correlationId`, `Receive`s with `timeout`. Both then run the **SAME `SuccessCriteriaChecker` and `OutputExtractor` as the HTTP path** (fed `$message` instead of `$response` via one added `"message"` context key) — no criteria/output logic is duplicated, and `$message` was already supported by the evaluator (Phase 5).
- **A send step is not special.** `successCriteria` and `outputs` work on **both** directions. Inside a `receive`, `$message` is the message that arrived; inside a `send`, it is the message that step published (same `{header, payload}` shape). That makes request/reply work without repeating an expression: a send records what it published (`outputs: {sentId: $message.payload.orderId}`) and a later receive correlates on `$steps.<send>.outputs.sentId`.

**The `Message` contract** (`adapter.go`) — the normalized shape every adapter speaks:

| field | meaning |
|---|---|
| `Payload` | the **decoded** body, backing `$message.payload` |
| `Headers` | backing `$message.header.*` |
| `Raw` / `ContentType` | the **serialized** form (best-effort here; formalized in Phase 10) |
| `Metadata` | adapter details (topic, transport) |

**`correlationId` is always honoured** — the phase's sharpest bug fix. It was only used when it was a
`$` runtime expression; a literal (`correlationId: "OP-2"`) evaluated to nil and the receive **silently
fell back to unfiltered**, returned an unrelated message, and reported SUCCESS. The four states now:

| `correlationId` | behaviour |
|---|---|
| absent / empty | unfiltered FIFO take, **with a warning** that it may consume a message this workflow did not expect |
| a literal (`OP-2`) | used **as the id** |
| a runtime expression that resolves | its resolved value is the id |
| a runtime expression that resolves to **nothing** | the step **FAILS** — *refusing to fall back to an unfiltered receive* |

That last row is the rule worth remembering: **a declared correlation that cannot be resolved is an
error, not a fallback.** Silently degrading to an unfiltered receive is worse than failing, because it
returns a plausible wrong message and calls it success.

**Other enforcement rules:**

| condition | outcome |
|---|---|
| `channelPath` without `action` | runtime **hard error** — direction undefined |
| step `action` contradicts the AsyncAPI operation's `action` | **operation wins**, plus a warning (the spec defines no conflict rule, so this does not hard-fail) |
| `timeout` absent or `<= 0` on a receive | defaults to **30s** |
| no message arrives in time | step fails; the span closes as an error carrying the reason |
| declared `successCriteria` | never silently skipped on **either** direction |
| adapter not configured (nil) | *AsyncAPI execution requires a configured adapter for this protocol* |

- **Async direction resolution for the LSP** — the validator only sees the Arazzo text, so it could classify a step as send/receive only when the step wrote `action:` itself. It now takes an optional resolver (`WithStepActionResolver`), which the server wires to the existing operation index ([definition.go](arazzo-designer-lsp/server/definition.go) `resolveStepAsyncAction`) using the same lookups navigation uses. That makes the direction of an `operationId`/`operationPath` step known and enables two editor diagnostics that previously could not fire on the spec-preferred form: **a receive with no `correlationId`**, and the **`action` vs operation-action mismatch** deferred from Phase 8. Both are warnings; **when the operation cannot be resolved the checks stay quiet** rather than guessing. *(These two diagnostics silently never fired until Phase 10 fixed an indexing race — see that phase.)*
- **Run-log parity with REST steps** — [async_telemetry.go](../../arazzo-designer-cli/internal/runner/executor/async_telemetry.go). A REST step emits a child `http` span (request on start, response on end) which the Logs tab renders under the step; an async step now emits the equivalent **`message` span** (new `telemetry.SpanKindMessage`), nested under the step span exactly as the HTTP span is.

**Message-span attributes** (the async analogue of the HTTP span; `messaging.content_type` was added in
Phase 10):

| attribute | on | analogue |
|---|---|---|
| `messaging.operation` (`send`/`receive`) | both | — |
| `messaging.channel` | both | `http.url` |
| `messaging.adapter` | both | — |
| `messaging.correlation_id` | receive | — |
| `messaging.timeout_ms` | receive | — |
| `messaging.message.body` / `.headers` | both | `http.request.body` / `http.response.body` — published up front on a send, reported **on arrival** for a receive |
| `messaging.content_type` | both (Phase 10) | `Content-Type` |

**Telemetry lives in the executor, not the adapters**, so adapters stay pure transport and every future
broker is instrumented for free — which is exactly what happened in Phase 11. The visualizer's Logs tab
collects `message` spans alongside `http` ones and renders them with the same card layout
([LogsTab.tsx](arazzo-designer-visualizer/src/views/WorkflowView/LogsTab.tsx) `MessagePairCard`).

Existing run telemetry drives the node red/green (received → success, timed out → failure). Examples
[examples/async_test/phase9/](../../examples/async_test/phase9) 01–09 all behave as documented (5 pass,
4 fail on purpose; `02` and `08` need their documented `$inputs`).

**Limits / deferred:**
- **The non-blocking receive model (b)** and the `dependsOn` "started-but-not-completed → wait-with-timeout"
  branch — Phase 14.
- **`operationPath` steps were not routed to the async path at all** here (`resolveAsyncTarget` only
  looked at `channelPath` and `operationId`), so the third targeting form failed with "Operation not
  found" even though the LSP navigated and validated it happily. Found and fixed in **Phase 10**.
- The in-memory adapter's correlation is **a value-match heuristic over the whole message**, not a
  schema-declared correlation location — it matches any scalar anywhere in the payload. Real
  correlation locations remain deferred past Phase 11.
- Serialization here is plain `json.Marshal` inline; the real serializer layer is Phase 10.
- The in-memory adapter carries the **decoded** `Payload` alongside `Raw`, so nothing exercises the
  deserialize path until a real broker exists (Phase 11).

<details><summary>Original design — kept for reference</summary>

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

</details>

### Phase 10: Message Serialization Layer — ✅ DONE (JSON + text; Avro/Protobuf stubbed)

Goal: separate message **shape** (headers/payload the runtime reasons about) from **wire format**
(the bytes a channel carries) so adapters don't each reinvent serialization.

**Implemented (CLI runner):**
- **`Serializer` interface + `SerializerRegistry`** — [serializer.go](../../arazzo-designer-cli/internal/runner/executor/serializer.go).
  `Serializer` = `Serialize`/`Deserialize` + `Name`/`ContentType`. The registry maps a content type
  to a serializer: empty → default JSON; `; charset=…` parameters stripped; case-insensitive; a
  `<x>+json` structured suffix → JSON; an **unknown content type is a hard error** (never guesses a
  wire format) naming the types that actually encode separately from the ones that are only
  recognized — listing a stub as "supported" would point the reader at a different failure.
- **Serializers:** JSON (`application/json`, default) and **text/plain** are fully implemented;
  **Protobuf** (`application/x-protobuf`, `application/protobuf`) and **Avro** (`application/avro`,
  `avro/binary`) are registered as **stubs** — they select cleanly and fail with a plain "not
  supported yet" rather than looking like a typo (real codecs land with the brokers in Phase 11).
- **Wired into the runtime** — [async_executor.go](../../arazzo-designer-cli/internal/runner/executor/async_executor.go):
  `executeSend` picks the serializer from the resolved content type and encodes the payload to
  `Message.Raw` (replacing the inline `json.Marshal`); `executeReceive` **deserializes `Raw` back
  into `$message.payload`** when the adapter delivers bytes-only. The in-memory adapter still carries
  the decoded `Payload`, so existing JSON workflows are byte-for-byte unchanged; the deserialize path
  is what a real broker (Phase 11) will exercise. Registry lives on `StepExecutor.Serializers`
  (default set from `NewDefaultSerializerRegistry`).
- **Content-type resolution follows the spec, not a hardcoded JSON default.** Arazzo §5.8.14.1: *"The
  Content-Type for the request content. If omitted then refer to Content-Type specified at the targeted
  operation to understand serialization requirements."* Send originally went straight from an absent
  step `contentType` to JSON, skipping the targeted operation entirely — so a step omitting the field
  published JSON onto a channel the AsyncAPI document declared as `text/plain` (`"hi"` with quotes
  instead of bare `hi`). Both directions now resolve **step `contentType` (send) / transport-carried
  type (receive) → AsyncAPI message `contentType` → document `defaultContentType` → JSON**, via
  `AsyncInfo.DeclaredContentType()`. Steps 2–3 are AsyncAPI's own precedence (*"When omitted, the value
  MUST be the one specified on the defaultContentType field"*); `defaultContentType` was previously
  ignored altogether. A channel's messages resolve **through `$ref`** (`$ref: '#/components/messages/x'`
  is how a real document is written; reading the channel map directly finds only a `$ref` key and would
  silently miss the declaration) — via the JSON Pointer resolver already used for channels and
  operations, in the runtime and in the LSP indexer alike. The step's value stays authoritative when present — the document is consulted
  only on omission — and a disagreement between the two logs a runtime warning.
- **Editor diagnostics for the same rule, in BOTH directions** (LSP). A receive chooses a serializer
  too, so it faces the same questions — only the advice differs, since it has no `requestBody` to
  settle anything in. Where **neither** the step nor the AsyncAPI document declares a content type,
  **information**: the message will be serialized (or decoded) as JSON — legal, but an assumption
  nothing in either document states. Where the document declares **more than one**, **warning**: which
  one will be used, plus how to settle it (`contentType` on the step for a send; one format per channel
  for a receive). And on a send whose `contentType` **disagrees** with the document, a warning that the
  step's value overrides the AsyncAPI declaration, so the published message
  will not match the format the channel's contract describes. Both need a fact outside the Arazzo text,
  so the validator takes a second injected resolver (`WithStepContentTypeResolver`, alongside Phase 9's
  action resolver — the two are now grouped as `diagnostics.StepResolvers`). Indexing resolves each
  channel's declared type at parse time (`ChannelInfo.ContentType`) and records the channel an
  operation targets (`OperationInfo.ChannelKey`), so `channelPath`, `operationId` and `operationPath`
  steps all reach the same declaration. A channel may declare SEVERAL formats (one per message), which
  the document cannot resolve on its own — both layers keep the whole set (`ChannelInfo.ContentTypes`,
  `AsyncInfo.DeclaredContentTypes`) rather than one value, so a third diagnostic warns that more than
  one is declared and names the deterministic pick, and a step naming the channel's SECOND format is
  correctly NOT reported as a disagreement. Comparison lives in `utils.SameMediaType`, shared by the
  indexer and the validator (the runner keeps its own copy — the modules cannot import each other). Media types are compared the way the runtime keys on them
  (parameters dropped, `+json` suffix → JSON), so equivalent spellings never report a mismatch.

- **A structured payload is not text.** `TextSerializer` stringified anything it didn't recognise, so
  an object payload on a text/plain channel went on the wire as Go's own map rendering
  (`map[kind:deploy note:v3]`) — unreadable to any consumer and silently wrong. It now fails with a
  clear message naming the fix. This became reachable *without the author writing `text/plain`
  themselves* once the AsyncAPI declaration started selecting the serializer for them, so the two
  changes ship together. Scalars (numbers, booleans) still stringify — they have an unambiguous text
  form. Registry lookups were also tightened: a `+json` suffix on a registry with no JSON serializer
  reports the content type as unsupported instead of returning a nil `Serializer` with a nil error, and
  `mustGet` panics on a missing alias target (a constructor-order programming error) rather than
  wrapping nil and failing at the first message that uses it.

**Tests:** `serializer_test.go` (registry selection incl. params/case/`+json`/unknown-error; JSON
round-trip + empty/invalid body; text round-trip; structured payload rejected; stub serializers fail
clearly; `+json` on a JSON-less registry) and `async_executor_test.go` cases (receive **deserializes
raw bytes** into `$message.payload`; text/plain send produces raw text; unsupported content type fails
at send; the full step → document → `defaultContentType` → JSON chain; `$ref`'d message declarations;
all three targeting forms resolving to the same channel and content type; an OpenAPI `operationPath`
staying on the HTTP path). LSP: `navigation` covers declared/`$ref`'d/absent content types and the
operation→channel link; `validator` covers both diagnostics incl. severity and normalization; and
`server/contenttype_test.go` drives the **real server resolvers end-to-end**, which is what exposed the
indexing race below. Examples cover **every scenario**:
[examples/async_test/phase10_serialization/](../../examples/async_test/phase10_serialization) — 01
text/plain, 02 JSON default, 03 unsupported-content-type (fails), 04 protobuf-stub (fails), 05
avro-stub (fails), 06 content-type normalization (`+json` suffix + `; charset` params → JSON), **07 the
AsyncAPI document deciding the format for a step that declares none** (two channels, two formats, one
workflow), **08 a `$ref`'d message declaration reached by `operationPath`**, **09 step/document
disagreement** (the step's value overrides the declaration, warned in both the editor and the run log), **10 document-level
`defaultContentType`** plus a message overriding it, **11 an object payload on a text/plain channel**
(fails), **12 all three targeting forms** reaching the same `$ref`'d declaration — the regression guard
for the `operationPath` routing fix — and **13 a channel declaring two different message formats**,
where the runtime guesses deterministically and says which step had to guess. Two AsyncAPI sources back them: `notifications.asyncapi.yaml`
(per-message declarations, one `$ref`'d, one untyped channel) and `telemetry.asyncapi.yaml` (root
`defaultContentType` + a message that overrides it).

**Which serializer ran is now reported, not just decided.** A workflow's outputs cannot reveal it —
the in-memory adapter hands the receive step the decoded payload alongside the bytes, so the output is
identical either way. Three places now say it explicitly:
- **send log**: the resolved encoder plus the exact bytes, quoted and length-capped
  (`as text/plain (9 bytes): "all clear"` vs `as application/json (6 bytes): "\"beta\""`) — the quoting
  is what makes a text `beta` (4 bytes) distinguishable from a JSON `"beta"` (6);
- **receive log**: `decoded as <content type>`, resolved through the same chain even when the payload
  arrived pre-decoded and no decode was needed — plus a warning when the decoder had to be GUESSED (the
  transport carried no content type and the channel declares several), which is the case that silently
  corrupts a payload against a real broker;
- **the run-log span**: a `messaging.content_type` attribute on both directions, which the visualizer's
  Logs tab renders in the Channel block as **Encoder** (send) or **Decoder** (receive), beside Adapter
  and Correlation ID. `timeout` was dropped from that block — it is a value declared on the step and
  already shown in the properties panel, not something the run produced; the span still carries it.

The README explains how to read all of it, and lists which steps should show the information/warning
diagnostics in the editor, so the LSP half is testable by hand too.
Scenario 02 targets a channel that declares nothing, so it exercises the JSON last resort rather than a
declared `application/json`. The one path not expressible as an example — receive-side deserialize of
raw bytes (real-broker path; the in-memory adapter always carries a decoded payload) — is covered by
unit tests, including the case where the bytes arrive with **no** content type and the AsyncAPI
declaration is the only thing that says how to decode them. All green (both module build/vet/test +
examples e2e).

**Diagnostics race closed here:** the validator's source-backed resolvers (Phase 9's action resolver
and Phase 10's content-type resolver) read the operation index, but `DidOpen`/`DidChange` start
indexing in a **background goroutine** and call `provideDiagnostics` immediately after — so on a fresh
open the index was usually still empty and every source-dependent check silently stayed quiet. Both
resolvers now call `ensureSourcesIndexed` (cache-backed, per-document lock) instead of
`resolveDocSources`, exactly as Definition/Hover already did, making the checks deterministic rather
than dependent on which goroutine wins. Found only by testing the resolvers end-to-end through the
server rather than layer by layer — the per-layer unit tests all passed while nothing worked in situ.

**Phase 9 gap closed here:** async steps could only be targeted by `channelPath` or `operationId` —
`resolveAsyncTarget` never looked at **`operationPath`**, so a step using the third targeting form was
not recognised as async at all, fell through to the HTTP path and failed with "Operation not found",
even though the LSP navigated, hovered and validated it happily. `AsyncFinder.FindOperationByPath`
resolves the pointer and follows the operation's channel `$ref` (shared with `FindOperationByID` via
`attachOperationChannel`), and returns nil for an OpenAPI operation — AsyncAPI 3.0 makes `action`
REQUIRED on an Operation Object and OpenAPI has no such field — so REST steps using `operationPath`
still reach the HTTP executor untouched.

**Deferred:** real Protobuf/Avro/CloudEvents codecs + schema-registry config (Phase 11 alongside the
brokers that need them); binary passthrough is not yet a distinct serializer (add when a broker needs it).

**Known gaps (end-cleanup batch, not blocking):** REST steps encode through
`httpexec.buildRequestBody`, a separate encoder that never consults the registry and whose rules are
looser (substring `"json"` match; an unknown content type is silently stringified rather than erroring;
form-encoding exists only there) — so the same `contentType` value can behave differently on a REST and
an async step. `serializerRegistry()` lazily assigns `se.Serializers` without a lock — harmless today,
a data race once Phase 14 runs steps in goroutines. `resolveDocSources` re-parses the Arazzo document
once per step during diagnostics (pre-existing; now also takes the per-document indexing lock), which
is wasted work on a large document — hoist it to one resolution per diagnostics pass. On send,
`Message.ContentType` is stamped with the serializer's canonical type, so a vendor type
(`application/vnd.order+json`) reaches the wire as `application/json`; no effect today since neither
MQTT 3.1.1 nor WebSocket carries the field. An empty `text/plain` payload serializes to zero bytes, so
on the real-broker path it decodes back to nil rather than `""`. The LSP does not flag a structured
payload on a text/plain channel — the runtime error is clear, but the editor could catch it earlier.

<details><summary>Original design — kept for reference</summary>

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

</details>

### Phase 11: Real Broker Adapters — ✅ DONE (WebSocket + MQTT; Kafka deferred)

Goal: real network transports behind the Phase-9 `Adapter` interface, selected from the AsyncAPI
document (Arazzo has no broker field — `servers.protocol`/`host` is the source of truth).

**Where the transport comes from, and why it has to be the AsyncAPI document.** An Arazzo workflow says
"send on this channel"; there is no field anywhere in the Arazzo spec that names a broker, a host, or a
protocol. AsyncAPI has exactly that, in its `servers` section. So the runner reads the transport out of
the *source description* the step targets, not out of the Arazzo document, and the runner itself never
learns anything broker-specific — everything below sits behind the Phase-9 `Adapter` interface.

**Implemented (CLI runner):**
- **Shared `messageBuffer`** — [adapter_buffer.go](../../arazzo-designer-cli/internal/runner/executor/adapter_buffer.go).
  Brokers deliver **asynchronously** (a subscription callback, a reader goroutine) while the runner
  consumes **synchronously** (a blocking `receive` with a timeout); this buffer is the queue between
  those two worlds. The per-channel FIFO + correlation matching + wait-until-deadline logic was
  extracted from the Phase-9 in-memory adapter so ALL adapters reuse one implementation rather than
  each reinventing it — `InMemoryAdapter` shrank to a thin `push`/`receive` wrapper. Correlation gained
  a **raw-bytes fallback** (`bytes.Contains` on `Raw`) for messages that arrive as bytes with no
  decoded payload, which is every message from a real broker.
- **`WSAdapter`** — [adapter_ws.go](../../arazzo-designer-cli/internal/runner/executor/adapter_ws.go)
  (gorilla/websocket). A WebSocket is a bidirectional pipe to one URL, so the **channel address maps to
  the URL path** and the adapter keeps **one connection per channel**
  (`ws(s)://host/<channel address>`). A reader goroutine drains every incoming frame into the buffer;
  `Send` writes **text frames** behind a write mutex (gorilla permits only one concurrent writer) with
  a write deadline; `wss` gets TLS from the dialer. A connection that errors is closed, dropped, and
  redialed on next use.
- **`MQTTAdapter`** — [adapter_mqtt.go](../../arazzo-designer-cli/internal/runner/executor/adapter_mqtt.go)
  (eclipse/paho.mqtt.golang v1.5.1). Channel address = MQTT **topic**; QoS 1 both ways; retain false.
  The load-bearing decision: **`Send` subscribes to the topic BEFORE it publishes.** Without that a
  `send` followed by a `receive` on the same channel can never work against a real broker — the message
  is published and gone before the receive step starts. Subscribing first means the broker echoes our
  own publication back to our own subscription and the receive finds it. The paho client sits behind a
  four-method `mqttClient` interface (`Connect`/`Publish`/`Subscribe`/`IsConnected`) purely so unit
  tests can substitute a fake and exercise the round trip with no network.
- **Adapter selection** — [adapter_select.go](../../arazzo-designer-cli/internal/runner/executor/adapter_select.go).
  `adapterFor(info)` reads the targeted source's `servers` and maps `protocol` to an adapter. The
  server is picked **first by sorted server name** — Go map iteration is randomized and the choice has
  to be repeatable across runs (the same hazard Phase 10 hit with message keys). Servers missing either
  `protocol` or `host` are skipped. Adapters are **cached per `protocol://host`** on
  `StepExecutor.asyncAdapters`, so every step against one broker shares a single connection.

**Protocol → transport mapping (exact forms).** `host` may carry an explicit `:port`; the default port
is applied only when it does not.

| `servers.<name>.protocol` | adapter (`Name()`) | connects to | notes |
|---|---|---|---|
| `ws` | `websocket` | `ws://<host>/<channel address>` | one connection per channel |
| `wss` | `websocket` | `wss://<host>/<channel address>` | TLS via the dialer |
| `mqtt` | `mqtt` | `tcp://<host>:1883` | channel address = topic |
| `mqtts`, `secure-mqtt` | `mqtt` | `ssl://<host>:8883` | AsyncAPI 3.0 spells TLS MQTT `secure-mqtt` |
| `kafka`, `kafka-secure` | — | — | **hard error**, planned future phase |
| *anything else* | — | — | **hard error**, unsupported protocol |
| *no `servers` section* | `in-memory` | nothing | Phase-9 in-process queues |

That last row is why **every Phase 9 and Phase 10 document, example and test kept working untouched**:
absent `servers` means the default adapter, which is the only one those phases ever had.

**Rules — what fails, what warns, what happens silently.**

| condition | outcome |
|---|---|
| `protocol: kafka` / `kafka-secure` | step fails: *the "kafka" protocol is not yet supported: a Kafka adapter (with Avro/Protobuf schema support) is a planned future phase …* |
| any other unrecognized `protocol` | step fails: *unsupported AsyncAPI server protocol "…" — supported: ws, wss, mqtt, mqtts (and in-memory when no servers are declared)* |
| no `servers`, or none with both `protocol` and `host` | in-memory adapter, **no message** — this is the supported Phase 9/10 mode, not a fallback worth warning about |
| broker connect / dial failure | step fails, naming the URL that was tried |
| MQTT connect, subscribe or publish exceeding 10s | step fails: *mqtt \<op\> timed out after 10s* |
| no matching message before the step's `timeout` | step fails via `ErrReceiveTimeout`, with **different wording** for a correlated wait (*no message matching correlationId "x" arrived*) than an uncorrelated one (*no message arrived*) |
| `Send` called with a nil message | adapter refuses rather than publishing an empty message |
| transport carries no content type on receive | resolved from the AsyncAPI document via the Phase-10 chain (see below); a **warning** when the channel declares more than one format |
| `correlationId` resolves to empty | **silent** — the receive becomes an unfiltered FIFO take (see the gotcha below) |

**Kafka is refused loudly and deliberately, not merely "not built yet."** It gets its own error rather
than falling into the generic unsupported-protocol branch because Kafka is precisely where Avro and
Protobuf are actually used — and those are still Phase 10's stubs. The `TODO` in `adapter_select.go`
couples them on purpose: **a Kafka adapter and real Avro/Protobuf codecs (plus schema-registry config)
land together**, because shipping the transport alone would give users a broker that cannot encode its
own ecosystem's formats.

**Content type on the receive path — where Phase 10 stops being theoretical.** MQTT 3.1.1 and WebSocket
carry **no content-type field at all** (MQTT 5.0 added one; a different client library). So on a real
broker the bytes arrive with no format label and the AsyncAPI document is the *only* thing that says
how to read them. Phase 10 built that decode path but nothing exercised it, because the in-memory
adapter always hands the receive step a pre-decoded payload. Phase 11 is where it does real work:
`executeReceive` resolves **transport-carried type → AsyncAPI message `contentType` (through `$ref`)
→ document `defaultContentType` → JSON**, warns when the channel declares several formats, and logs
`decoded as <content type>` either way.

**Correlation matching, in precedence order** (`messageMatchesCorrelation`). An empty id matches
anything; otherwise the first hit wins:
1. `Metadata["correlationId"]` equals the id;
2. any header value equals the id;
3. any scalar anywhere in the decoded payload equals the id (recursing through maps and slices);
4. **for bytes-only messages** (every real-broker message): the id appears as a **substring of `Raw`**.

Step 4 is a deliberately blunt heuristic — it can match a token that happens to appear anywhere in the
body, including inside an unrelated field. Correlation from schema-declared locations is deferred.

**Gotcha worth remembering: a correlation that does not MATCH is indistinguishable from no message at
all.** The four `correlationId` states are Phase 9's (a bare literal **is** honoured as the id; only an
*absent* one means unfiltered FIFO, and an expression resolving to nothing is a hard error). What bites
on a real broker is the fifth situation, which is not an error state at all: the id resolves fine but
**no arriving message carries it**, so every message is skipped and the step times out looking exactly
like a dead channel. Running an example with the wrong `$inputs` value reproduces this — see
`06-mqtt-text-plain`, whose payload hardcodes `txt-8`, so any other token times out. The public-broker
examples depend on this filtering to ignore strangers' traffic on the shared topic and the WebSocket
server's greeting banner.

**Fixed values and defaults** (all constants, none configurable yet):

| value | setting |
|---|---|
| MQTT connect / subscribe / publish timeout | 10s (`mqttOpTimeout`) |
| MQTT QoS (publish and subscribe) | 1 |
| MQTT retain | false |
| MQTT client id | `arazzo-runner-<unix nanos>`, clean session |
| WebSocket dial / write timeout | 10s each |
| WebSocket frame type | text |
| buffer poll interval | 10ms |
| receive timeout when the step declares `<= 0` | 30s |

**Tests** ([adapter_phase11_test.go](../../arazzo-designer-cli/internal/runner/executor/adapter_phase11_test.go)) —
11 tests, all runnable with no network: buffer raw-byte correlation; WS round trip, connect failure,
and a **full `ExecuteStep` end-to-end against a real local WebSocket echo server** (`httptest`); MQTT
subscribe-before-publish, round trip, timeout and broker-URL mapping through the fake client; adapter
selection incl. kafka/unknown errors, in-memory fallback and per-broker caching. Plus an **opt-in real
broker integration test** gated on `ARAZZO_TEST_MQTT_BROKER` (verified green against
broker.hivemq.com), so CI never depends on public infrastructure.

**Examples** — [examples/async_test/phase11/](../../examples/async_test/phase11). Unusually, the network
ones run against the **real public internet**; all ten were re-verified after the Phase-10 restack.

| file | shows | expected |
|---|---|---|
| `01-mqtt-roundtrip` | MQTT publish + subscribe via `channelPath` (HiveMQ) | ✅ `sentBack = 21.5` |
| `02-ws-echo` | WebSocket send + receive over `wss` (echo.websocket.org) | ✅ `echoed` |
| `03-kafka-unsupported` | `kafka` protocol | ❌ planned-future error |
| `04-mqtt-operationid` | MQTT via `operationId` (direction from the operation's `action`) | ✅ `got = 19` |
| `05-mqtt-timeout` | MQTT receive on a quiet topic | ❌ *timed out after 3s: no message arrived* |
| `06-mqtt-text-plain` | **text/plain over MQTT** — the decoder can only come from the document | ✅ bare-string `heard` |
| `07-mqtts-tls` | MQTT over TLS (`mqtts` → `ssl://…:8883`) | ✅ `sentBack = 23.7` |
| `08-ws-timeout` | WebSocket receive with no matching frame | ❌ *no message matching correlationId* |
| `09-unknown-protocol` | `amqp` protocol | ❌ unsupported-protocol error |
| `10-inmemory-fallback` | no `servers` → in-memory, no network | ✅ |

The networked examples need their documented `$inputs` token (`06` uses `txt-8`, the rest `demo-42`);
running them without inputs produces a correlation-filter timeout, not a transport failure — the same
expression gotcha above, and a good reminder that **an example run without its inputs is not a failing
example**. Re-verified after the restack: Phase 11 10 flows (6 complete, 4 failing by design), Phase 10
14 flows (10 / 4), Phase 9 9 flows (5 / 4) — the in-memory path is untouched by this phase.

**Restacked onto Phase 10 (2026-08-14).** Phase 11 was written against a Phase 10 that then moved 52
commits, so it was rebased. Two files conflicted:
- **`async_executor.go`** — seven hunks, all the same collision: Phase 10 rewrote the content-type
  logic in exactly the regions where Phase 11 threaded an `adapter` parameter through. Resolved
  uniformly by keeping Phase 10's logic with Phase 11's adapter; the merged signatures now carry both
  (`executeSend(step, adapter, info, channel, state, stepID, parentSpanID)`, same for `executeReceive`).
- **`asyncapi_plan.md`** — Phase 11's plan commit described Phase 10 in its pre-fix state; Phase 10's
  current text won, keeping Phase 11's link paths (three links here are missing their `../../` prefix
  on the Phase-10 branch and are correct here).

The one judgment call: Phase 11's receive fell back to its own `channelMessageContentType(info)` when
the transport carried no content type. Phase 10 had independently built a fuller chain for the same
problem — `AsyncInfo.DeclaredContentTypes()`, which follows `$ref`s, honours `defaultContentType`, and
warns on ambiguity — so **`channelMessageContentType` was dropped from the call path in favour of it**.
Example 06 is the proof, since MQTT carries no content-type field: it logs `decoded as text/plain` and
returns a bare string rather than failing a JSON parse. **`channelMessageContentType` in
`adapter_select.go` is now dead code** — no production callers, only its own unit test — and should be
deleted along with `TestChannelMessageContentType`.

**Deferred (TODO in adapter_select.go):** Kafka adapter + real Avro/Protobuf codecs with
schema-registry config (they belong together, see above); MQTT credentials and custom TLS
configuration; correlation from schema-declared locations.

**Known gaps / limits (not blocking):**
- **Adapters are never closed.** There is no shutdown path — no `Close`, no `Disconnect`, no
  `Unsubscribe`. MQTT connections and WebSocket connections live until the process exits (a WS
  connection is dropped only when it errors). Fine for a CLI run, a leak for a long-lived server.
- **`StepExecutor.asyncAdapters` is written without a lock**, same class of problem as
  `serializerRegistry()` — harmless while steps run sequentially, a data race once Phase 14 runs them
  in goroutines. Both should be fixed together.
- **`secure-mqtt` works but is not advertised**: `adapterFor` accepts it, yet both error messages list
  only "ws, wss, mqtt, mqtts", so a user who mistypes it is told the correct spelling is unsupported.
- **The buffer polls rather than signals** (10ms), so every receive has a latency floor and burns a
  little CPU while waiting. A condition variable would fix both.
- **MQTT send is self-echoing by design.** Because `Send` subscribes first, the sender's own message
  comes back to it — which is what makes single-workflow round trips work, but means a receive step
  cannot distinguish "my own message" from "a peer's" except via `correlationId`.
- **Subscription is LAZY, so there is a window in which messages are missed.** `ensureSubscribed`
  (MQTT) / `ensureConn` (WS) are called only from `Send` and `Receive`, i.e. by **the first step that
  touches a channel** — there is no workflow-start hook. In `send→A, receive←A, receive←B`, channel B
  is not subscribed until the third step runs, and anything a peer published to B before that moment is
  lost permanently (MQTT does not replay to late subscribers; we publish with `retained: false`). Note
  that `Send` subscribing first is not a decision about where subscription belongs — it exists so the
  broker echoes our own publication back — so channel A being covered early is incidental.
  **The `messageBuffer` does NOT close this gap**: it queues what a subscription delivered, so it only
  covers "arrived after subscribing but before the receive step ran". The fix is to resolve every
  step's target up front and **subscribe to all channels the workflow will use before step 1 runs**,
  shrinking the window to the workflow's own start. That needs `resolveAsyncTarget` per step before
  executing any (a channel reached by `operationId`/`operationPath` is not literal on the step), moves
  connection errors to workflow start rather than the offending step, and for WebSocket means dialing —
  and therefore receiving the server's greeting frame — earlier.
- **The AsyncAPI Correlation ID Object is never read.** AsyncAPI 3.0 lets a message declare where the
  id lives (`correlationId.location`, e.g. `$message.header#/correlationId`), which is the contract
  publishers and subscribers are supposed to share. Nothing in the runtime reads it: `asyncapi_finder.go`
  does not mention `correlationId` at all. Consequently the **send** side puts the id nowhere in
  particular (the examples just happen to include a `token` field), the **receive** side searches the
  whole message, and **nothing validates** that a sent message carries a correlatable value at all — a
  mismatch only surfaces as a receive timeout. Combined with the raw-bytes substring fallback this can
  match the wrong message outright: with id `42`, the body `{"orderId":"99","note":"see ticket 42"}`
  matches. Implementing it properly means reading the Correlation ID Object (through `$ref`), evaluating
  its `location` against the arriving message, **and** using it on the send side to place the id
  automatically. Phase 9's example `02-correlation` already predicted this would land in Phase 11; it
  did not.
- **No QoS, retain, consumer-group or keepalive configuration** — all fixed constants. No WebSocket
  ping/pong keepalive or reconnect backoff either.
- **The networked examples depend on public infrastructure** (broker.hivemq.com, echo.websocket.org)
  and on the topics being quiet enough that correlation filtering succeeds. The unit tests do not.
- Phase 10's own gap list still applies to the async path (REST steps encoding through a separate
  looser encoder; empty `text/plain` decoding back to nil rather than `""`).

<details><summary>Original design — kept for reference</summary>

Goal: one production-grade adapter after the generic runtime is proven. Candidates: WebSocket
(easiest demo), Kafka (enterprise streaming), MQTT (IoT). Responsibilities per adapter: map
AsyncAPI channel → topic/exchange, publish & consume, match `correlationId` from headers/payload,
use the serializer registry, support auth/TLS (and consumer groups / QoS / routing keys as
applicable).

Tests: integration tests behind opt-in env vars; unit tests with mocked broker clients;
end-to-end sample for the chosen broker.

</details>

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

### Phase 13: Visualizer UI Enhancements — ❌ NOT STARTED (⚠️ needs TEAM CONFIRMATION first)

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

### Phase 14 (FINAL): Non-Blocking Async Steps — ❌ NOT STARTED

Goal: run an async step **concurrently** with the rest of the workflow, and make `dependsOn` the
point where the workflow actually waits for it. This replaces the Phase-9 blocking model (choice (a))
with the deferred model (b).

**Why this matters.** Phase 9 waits inline, so a workflow halts on an async step even when the steps
that follow have nothing to do with it:

```
step1  async receive        blocking (today): everything stops here, up to `timeout`
step2  REST                 non-blocking (Phase 14): runs immediately, in parallel
step3  REST
step4  REST
step5  REST, dependsOn: [step1]   <- the ONLY place the workflow should wait
```

Worse, blocking makes step-level `dependsOn` almost meaningless for its stated purpose: by the time
step 5 is reached the dependency has already settled, so the "gate" never gates anything. The spec is
explicit that this field exists for the concurrent case:

> "The `dependsOn` field at the step level is primarily intended to coordinate asynchronous
> operations." … "When a step must wait for an asynchronous operation to complete before proceeding,
> `dependsOn` establishes a join point for **in-flight** async work."

*(Verify this second quote against the rendered spec before implementing — it was read via a summarizing
fetch, unlike the first.)*

**Design decisions (agreed):**
1. **No new step status is needed.** `models.go` already defines `StepStatusPending`, `StepStatusRunning`,
   `StepStatusSuccess`, `StepStatusFailure`, `StepStatusSkipped`. An in-flight async step is
   `StepStatusRunning`; it becomes success/failure when its goroutine settles.
2. **The step function returns immediately; the goroutine does not.** `ExecuteStep` spawns a goroutine
   that performs the `Receive` (up to the step's `timeout`) and returns a "started" result right away.
   The goroutine is the long-lived part — it lives for the whole receive, not briefly.
3. **`checkStepDependencies` becomes a join.** Today it reads a status; it must instead: if the
   dependency is `Running`, block until that goroutine settles (bounded by the async step's own
   `timeout`), then apply the existing Phase-7 rule — success ⇒ proceed, failure/timeout ⇒ hard error,
   which fails the dependent step and the workflow.
4. **Nothing is left dangling.** If no step ever joins an in-flight step, the workflow waits for it at
   the end: every spawned goroutine must settle (success or timeout) before the workflow completes.
5. **Telemetry stays correct, ordering gets interleaved.** Step spans will overlap in wall-clock time,
   which is expected for concurrent work. Parent/child links are unaffected because `ParentID` is set
   explicitly (`state.WorkflowSpanID`), not derived from emission order; per-step attributes/outputs are
   emitted exactly as today, just when the goroutine settles rather than inline.
6. **No new graph work.** `BaseNodeWidget` already renders `traceState: 'running' | 'passed' | 'failed'`
   (running = `ThemeColors.PRIMARY`), so an in-flight async step reuses the existing running animation
   and turns green/red on settle. This is the one part of Phase 14 that needs **no** change.

7. **Only `receive` goes async; `send` stays inline.** A publish has no waiting semantics, so making it
   concurrent buys nothing and only adds ordering surprises.
8. **An output reference without `dependsOn` is a USER ERROR, not an implicit join.** If a step reads
   `$steps.<asyncStep>.outputs.x` while that step is still `Running`, the runtime does **not** silently
   wait — it emits a **warning** naming both steps and telling the author to declare `dependsOn`. The
   value resolves as it does today (nil), so behavior is unchanged; the author is simply told why.
   *(Optional extra: the LSP could flag this statically — a step referencing an async step's outputs
   without listing it in `dependsOn` — but the runtime warning is the agreed requirement.)*
9. **Async step outputs are stored like any other step's.** An async step is not special: its result
   lands in `state.StepsData`/`StepsStatus` when the goroutine settles and stays readable, so two
   steps joining the same async step both observe the same settled outcome. Nothing is consumed once.
10. **Cancellation is one-directional.** If the **workflow** fails at some step, all in-flight
    goroutines are cancelled — there is nothing left to wait for. But a **goroutine failing does NOT
    fail the workflow**: an async step that times out only causes a failure where something actually
    `dependsOn` it. An unjoined failure is still recorded (status + telemetry) but does not abort the run.
11. **Goroutine lifetime = until the receive settles.** It ends as soon as a matching message arrives
    **or** the timeout elapses, whichever comes first — it never lingers past that. This already falls
    out of `Adapter.Receive`, which returns on the first match or `ErrReceiveTimeout`.

**Unresolved — decide before implementing:**
- **`goto`/retry jumping backwards past an in-flight async step**: does that re-run the async step, or
  join the one already running? Deliberately left open.

**Implementation note (important).** Today the runner is effectively single-threaded, so
`ExecutionState` (`StepsData`, `StepsStatus`, outputs) is written without synchronization. Once async
steps settle from goroutines, that state becomes **shared mutable state across goroutines** and must be
guarded (a mutex on `ExecutionState`, or funnelling results through a channel the main loop drains).
A concurrent map read/write in Go is a fatal runtime error, not a recoverable one — so this is a
correctness requirement, not an optimization.

**Sequencing.** Do this LAST, after Phase 11. It is only *meaningful* against a real broker: on the
in-memory adapter a receive can only ever return a message the workflow itself sent, so there is
nothing genuinely in-flight to overlap with. It also pairs naturally with Phase 13's status rendering.

Tests: an async step followed by unrelated REST steps does not delay them; a later `dependsOn` step
waits for the in-flight step and then runs; a timed-out async step fails its dependents; a timed-out
async step that nothing depends on does NOT fail the workflow; a workflow failure cancels in-flight
goroutines; a workflow with a never-joined async step still waits for it before completing; two steps
depending on one async step both see the same result; referencing a running step's outputs without
`dependsOn` warns; telemetry keeps correct parent/child links with overlapping spans; the whole suite
runs clean under `go test -race` (the shared-state requirement above).

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
confirmation) and Phase 14 (non-blocking async steps) are the last two — Phase 14 last of all, since
it only becomes meaningful once Phase 11 provides a real broker to wait on.

## Known Issues / Bugs (separate from the v1.1.0 phases — fix independently)

> **End-of-project cleanup batch.** None of these are v1.1.0 phase work. Best tackled together at the
> very end, after Phases 1–12, in one final pass: (1) the final XPath push (XPath selectors + `targetSelectorType: xpath`, see Phases 4/6), (2) the server-stop UI bug below, (3) executable `type: arazzo` source descriptions below, and (4) the two remaining LSP validation blind spots below (goto target existence; $steps refs outside parameters).

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

### GAP: two remaining LSP validation blind spots (missed detections, not false alarms)
**Found while auditing the validator for misfires during Phase 9.** Items 1–2 are still open: cases the
validator stays silent on when it arguably shouldn't — the opposite of a false positive, so nothing
reports incorrectly today. Additive new rules; fold into the end-of-project batch. Item 3 is kept,
struck through, because it was the root cause the other deferred async checks shared.

1. **A `goto` to a non-existent `stepId` is never validated.** `onSuccess: [{type: goto, stepId:
   doesNotExist}]` produces no diagnostic at all, so a typo there only surfaces at runtime. Both the
   step list and the workflow list are already available to the validator, so this is a small check —
   the same shape as the existing `dependsOn` reference validation.
2. **`$steps.<id>` references are only checked inside `parameters`.** The identical reference in
   `outputs` or `successCriteria` is not validated, so a typo'd step id there is silently unresolved.
   Coverage should be consistent across the three positions.
3. ~~**Async step checks can't see the source document.**~~ **FIXED** (see "Async direction resolution"
   in Phase 9 above). The validator now receives an optional step-action resolver, so an
   `operationId`/`operationPath` step's direction is resolved from the indexed AsyncAPI operation.
   This closed both the "receive with no `correlationId`" gap on that form **and** the
   `operationId`/`action` mismatch diagnostic deferred from Phase 8.

Related and already fixed (recorded so the distinction is clear): the `$steps.<id>` check used to be
a hard **error** whenever the referenced step was declared later, which false-positived on a legal
backward-`goto` loop — declaration order is not execution order. It now errors only when the step does
not exist, and warns when it exists but is declared later.

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
