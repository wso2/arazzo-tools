# Phase 2 — Validation check examples

These files let you **visually verify the Phase 2 LSP validation** by opening them in VS Code
with the Arazzo extension installed. The validation is *static* (it does not run anything), so
you only need to open each file and look at the squiggly underlines in the editor / Problems panel.

| File | What it shows | Expected result |
|---|---|---|
| `00-valid.arazzo.yaml` | A fully-correct v1.1.0 document | **No diagnostics** — zero red, zero yellow |
| `01-errors.arazzo.yaml` | One RED error per step/field | 14 red errors (see comments in the file) |
| `02-warnings.arazzo.yaml` | YELLOW warnings | 4 yellow warnings |
| `03-unknown-fields.arazzo.yaml` | Misspelled / wrong field names | 3 yellow "unknown field" warnings (and `x-` ignored) |

## How to verify

1. Open the folder in VS Code with the Arazzo extension active.
2. Open each file. Look at the **Problems panel** (View → Problems) and the squiggles in the editor.
3. Compare against the `# ERROR:` / `# WARNING:` comments written on each line.

## Things to know about WHERE the underline appears

- **Unknown-field warnings** (`03-...`) are drawn on the **exact line** of the bad field name.
- **Step validation errors/warnings** (`01-...`, `02-...`) are drawn on the step's **`stepId:` line**
  (that is how the validator reports step problems). Hover the squiggle to read the precise message,
  which names the exact field and rule.
- **Document-level errors** (`$self` fragment, bad source `type`) are drawn at the **top of the file**.

## Quick reference — what each rule catches

**Errors (red):** `$self` containing `#` · invalid source `type` ·
step with zero or more-than-one target · invalid `action` value · `channelPath` not pointing at an
`asyncapi` source · negative `timeout` · empty `successCriteria: []` · invalid parameter `in` value ·
`dependsOn` to an unknown step · invalid action `type` · action with both `stepId` and `workflowId` ·
`in` used on action parameters · `reference` that doesn't resolve to a component.

**Warnings (yellow):** `action` without `channelPath` · `correlationId` on a non-`receive` step ·
`correlationId` without `channelPath` · `channelPath` to an unknown source · any unknown field name.
