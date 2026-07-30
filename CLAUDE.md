# CLAUDE.md — arazzo-tools

Guidance for Claude Code when working in this repository.

## What this repo is

VS Code extension + tooling for the OpenAPI Initiative's **Arazzo Specification** (WSO2-based).
Currently being upgraded from Arazzo v1.0.1 → **v1.1.0** (AsyncAPI support, `$self`, Selector
Objects, step `dependsOn`, etc.).

**The plan / source of truth for the upgrade:** `extensions/arazzo-visualizer/asyncapi_plan.md`
— phased plan (Phases 1–12), per-phase status headers, and a "Known Issues / Bugs" section that
holds the end-of-project cleanup batch. Read it before doing any v1.1.0 work.

## Layout (the parts that matter)

- `arazzo-designer-cli/` — **Go module** (`github.com/wso2/arazzo-designer-cli`). The workflow
  runner/engine + MCP server. Key packages:
  - `internal/evaluator/` — runtime expressions (`$steps`, `$inputs`, `$self`, `$sourceDescriptions`,
    Selector Objects, condition evaluation `EvaluateSimpleCondition`), JSONPath via `ohler55/ojg`.
  - `internal/loader/` — Arazzo doc + source-description loading, `$self`/base-URI resolution (`resolve.go`).
  - `internal/runner/` — `ExecuteWorkflow` orchestration, workflow-level `dependsOn` (triggers deps),
    step-level `dependsOn` gate, `ExecutionState` population.
  - `internal/runner/executor/` — step execution, parameters/body prep (`parameter_processor.go`,
    incl. payload `replacements`), success criteria, output extraction, operation lookup.
- `extensions/arazzo-visualizer/arazzo-designer-lsp/` — **Go module** (`github.com/arazzo/lsp`).
  The language server: `validator/` (incl. `unknown_fields.go`), `completion/`, `parser/`.
- `extensions/arazzo-visualizer/arazzo-designer-extension/` — the VS Code extension (TypeScript).
  Loads the CLI binary from `<extension>/cli/` and the LSP from `<extension>/ls/` at runtime
  (see `src/mcp/mcpServerRunner.ts`, `src/extension.ts`).
- `extensions/arazzo-visualizer/arazzo-designer-core/` — TS interfaces (`arazzoInterface.ts` is the
  v1.1.0 model reference).
- `examples/async_test/` — per-phase runnable/verification examples (`phase1/`, `phase2_validation/`,
  `phase3_selfResolution/`, `phase4_selectors/`, `phase5/`), each with a README. They use the live
  Toolshop API (`api.practicesoftwaretesting.com`) — internet required.

## Building & testing

- **Go modules** (do this for verification after every change):
  ```
  cd arazzo-designer-cli            && go build ./... && go vet ./... && go test ./...
  cd extensions/arazzo-visualizer/arazzo-designer-lsp && go build ./... && go vet ./... && go test ./...
  ```
- **Windows quirk:** `go test` may fail with "Access is denied" on testlog.txt unless you set a
  workspace-local temp dir first: `export GOTMPDIR="$PWD/.gotmp" TMP="$PWD/.gotmp" TEMP="$PWD/.gotmp"; mkdir -p "$GOTMPDIR"`
  (remove `.gotmp` afterwards). This is environmental, NOT a real test failure.
- **CLI binary only:** `pnpm --dir extensions/arazzo-visualizer/arazzo-designer-extension run build-cli`
  (or `arazzo-designer-cli/build-binaries.ps1`). Both output all-platform binaries into
  `extensions/arazzo-visualizer/arazzo-designer-extension/cli/` — that's where the extension loads from.
- **Full build:** `rush build` (Rush builds the TS projects; the extension's `build` script chains
  webpack → `package` → `prepare-binaries` (build-cli.js + build-lsp.js) → VSIX).
- The repo is not gofmt-clean overall (CRLF line endings); don't mass-reformat files.

## Key v1.1.0 design decisions (do not re-litigate; rationale in asyncapi_plan.md)

- **Step `dependsOn` = completion GATE** (spec §5.8.5.1): no reordering, no triggering. Unmet
  prerequisite → hard error. "Completed" = ran + terminal success. Workflow-level `dependsOn` is
  different: it DOES trigger dependency workflows (spec §5.8.4.1 has no "does not trigger" clause).
- **Selector Object**: all three fields (`context`, `selector`, `type`) are REQUIRED (spec §5.8.13.1).
- **Expression Type Object**: `version` is REQUIRED in the object form (§5.8.12.1); the bare-string
  short form (`type: jsonpath`) takes the spec default version. CLI and LSP must agree.
- **Criterion string `type`** allows `simple | regex | jsonpath | xpath` — a DIFFERENT set from the
  Expression-Type dialects (`jsonpath | xpath | jsonpointer`). Don't validate one against the other.
- **`$sourceDescriptions.<name>.<ref>`** resolves per §5.9.2 priority: operationId/workflowId match
  first, then Source-Description-Object field. (The operationId/operationPath *targeting* forms are a
  separate, older code path in `operation_finder.go`.)
- **Fail safe, don't leak:** on selector/expression failures, return nil / skip — never pass the raw
  `{context,selector,type}` descriptor or silently no-op a replacement.
- **Deferred to ONE end-of-project batch** (see plan "Known Issues / Bugs"): XPath engine (Phase 4
  selectors + Phase 6 `targetSelectorType: xpath`), the server-stop UI state bug, executable
  `type: arazzo` source descriptions (pre-existing v1.0.1 gap), cross-document `dependsOn` execution.
- Always verify behavior against the official spec: https://spec.openapis.org/arazzo/latest.html —
  quote the section when making a spec-based decision.

## Working conventions (established with the maintainer of this fork)

- **Never commit or push without being asked.** The user reviews diffs and often commits manually
  to a branch of their choosing. When asked to commit: separate commit per logical fix.
- **No `Co-Authored-By` / generated-with footers in commits.**
- AI review suggestions (Copilot/CodeRabbit): **verify each against the current code and the spec
  first**; fix only genuinely valid ones, minimally, one commit each with message
  `[suggestion fix] <what/why>`; give a paste-ready decline comment for rejected ones.
- Branch flow: stacked branches `asyncV1-phase2..5...` (fork `HimethW/arazzo-tools`, upstream
  `wso2/arazzo-tools`). PRs stack on the previous phase branch; after upstream squash-merges,
  rebase the next branch onto upstream main (`--force-with-lease`).
- After implementing a phase: verify with build/vet/full tests on BOTH Go modules, add runnable
  examples under `examples/async_test/phase<N>/` with a README, and update `asyncapi_plan.md`
  status + the per-phase notes.
- The user is newer to Go — when asked to explain, explain simply with concrete examples first.
