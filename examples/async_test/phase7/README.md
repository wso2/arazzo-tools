# Phase 7 — Step dependencies (`dependsOn`) examples

These workflows exercise **Phase 7**: the step-level `dependsOn` **completion gate** and the
**cycle detection** for both step-level and workflow-level `dependsOn`. Most run against the live
**Toolshop** REST API (`api.practicesoftwaretesting.com`), so **internet is required** for the
passing ones (the failing ones are caught before any HTTP call).

> Reminder (spec §5.8.5.1): step `dependsOn` is a **prerequisite gate** — it does **not** reorder
> steps and does **not** trigger the referenced step. An unmet prerequisite is a **hard error**.
> Workflow `dependsOn` is different — it **does** trigger the dependency workflow (spec §5.8.4.1).

## The scenarios

| File | What it shows | Expected result |
|---|---|---|
| `01-step-dependson-satisfied.arazzo.yaml` | step dependsOn a prerequisite that already ran | ✅ **passes** (both steps run) |
| `02-step-dependson-unmet.arazzo.yaml` | step dependsOn a step defined **later** (not run) | ❌ **fails on purpose** — `dependsOn … has not completed successfully` |
| `03-workflow-dependson.arazzo.yaml` | workflow dependsOn → dependency workflow is **triggered** first; read via `$dependencies` | ✅ **passes** (run `mainFlow`) |
| `04-workflow-cycle.arazzo.yaml` | circular **workflow** dependsOn (alpha↔beta) | ❌ **fails on purpose** — `circular workflow dependsOn detected` (no crash) |
| `05-local-step-cycle.arazzo.yaml` | circular **step** dependsOn (a↔b) in one workflow | ❌ **fails on purpose** — red squiggle in the editor **and** a failed run |
| `06-cross-workflow-dependson.arazzo.yaml` | cross-workflow step dep `$workflows.<wf>.steps.<s>` after the workflow ran | ✅ **passes** (run `consumer`) |
| `07-cross-workflow-step-ran.arazzo.yaml` | cross-workflow dep on a **specific** step (`secondProvide`) that actually ran | ✅ **passes** (run `consumer`) |
| `08-cross-workflow-step-skipped.arazzo.yaml` | cross-workflow dep on a step the dependency workflow **skipped via `goto`** (workflow ran, step didn't) | ❌ **fails on purpose** — `step 'skippedStep' in workflow 'provider' did not complete successfully` |

## How to run / verify

Open a file in VS Code with the Arazzo extension and run the workflow (the same way as the other
`examples/async_test/*` folders). No inputs are needed for any of these.

- **Passing (✅):** `01`, `03` (run `mainFlow`), `06` / `07` (run `consumer`) → the workflow completes.
- **Failing on purpose (❌):** `02`, `04`, `05`, `08` (run `consumer`) → the run ends in an error with
  the message shown above. That error **is** the expected outcome — it's the gate / cycle detection
  doing its job.
- **`05` also shows the LSP check:** just opening it should put a **red error** on the step
  (`circular dependsOn detected …`) before you even run it.

> ⚠️ If you run these through the VS Code extension, make sure the CLI binary is up to date
> (`pnpm --dir extensions/arazzo-visualizer/arazzo-designer-extension run build-cli`) — otherwise the
> extension uses an older binary that may not have the Phase 7 gate/cycle-detection.

## Notes

- **What "completed" means:** a prerequisite counts only if it ran and reached terminal **success**.
  A prerequisite that ran but *failed* does not satisfy the gate. (This case is covered by the unit
  test `runner.TestCheckStepDependencies`; it isn't a standalone example because a failed step usually
  halts the workflow before the dependent is reached.)
- **No reordering:** scenario `02` fails rather than silently running `runsLater` first — that's
  deliberate. The runner never reorders steps or triggers a dependency step.
- **Cross-workflow deps are step-specific:** a `$workflows.<wf>.steps.<s>` dependency checks that
  the **specific step `s`** reached success — not merely that `<wf>` ran. Scenario `07` passes
  because the exact step ran; `08` fails because the step was skipped by a `goto` even though the
  workflow completed. (Unit-tested in `runner.TestCheckStepDependencies`.)
- **Deferred:** cross-**document** `dependsOn`
  (`$sourceDescriptions.<name>.<wf>.steps.<s>`) is validated but not executed yet, and the AsyncAPI
  "wait for a non-blocking step to complete (with timeout)" behavior lands with the AsyncAPI runtime.
