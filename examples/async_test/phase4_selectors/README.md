# Phase 4 — Selector Object check examples

These workflows exercise **Selector Objects** (`{context, selector, type}`) in **every position**
the v1.1.0 spec allows one, all against the real **Toolshop** REST API
(`api.practicesoftwaretesting.com`). Each file isolates one position so you can see selector
evaluation working there.

> All call the live API, so **internet is required**.

## The five scenarios

| File | Selector position | Where it's evaluated in the runner |
|---|---|---|
| `01-step-output-selector.arazzo.yaml` | **step outputs** | `output_extractor.go` |
| `02-workflow-output-selector.arazzo.yaml` | **workflow outputs** | `runner.go` (`resolveWorkflowOutputs`) |
| `03-parameter-value-selector.arazzo.yaml` | **parameter value** | `parameter_processor.go` (`resolveParameterValue`) |
| `04-payload-field-selector.arazzo.yaml` | **request-body payload field** (nested) | `parameter_processor.go` → `evaluator.processValue` |
| `05-replacement-value-selector.arazzo.yaml` | **payload replacement value** (contrived: PLACEHOLDER payload) | `parameter_processor.go` (`applyReplacements`) |
| `06-fetched-payload-patch.arazzo.yaml` | **payload replacement value** (realistic: patch a payload supplied via `$inputs.order`) | `parameter_processor.go` (`applyReplacements`) |
| `07-jsonpath-replacement-target.arazzo.yaml` | **replacement TARGET via `targetSelectorType: jsonpath`** (the *where*, not just the *what*) | `parameter_processor.go` → `evaluator.SetJSONPath` |

Each one uses both selector dialects somewhere: **JSON Pointer** (`type: jsonpointer`, e.g. `/0/id`)
and **JSONPath** (`type: jsonpath`, e.g. `$[0].name`, `$[*].name`, `$.data[0].id`). Scenario 1 also
keeps a plain string expression (`$response.body#/0/id`) next to a selector so you can compare them.

Data shapes used: `getBrands` returns a **top-level array** of `{id, name, slug}`; `getProducts`
returns `{ data: [ {id, name, price}, ... ] }`.

## How to run

Run them the **same way as `examples/go-runner-test/toolshop`** — open a file in VS Code with the
Arazzo extension and run the workflow (under the hood: `arazzo-designer-cli serve -f <file>`).

- `01`, `02`, `03` use only **GET** endpoints (brands) — no inputs needed.
- `04`, `05` use the **cart flow** (`getProducts` → `createCart` → `addItem`), mirroring
  `go-runner-test/toolshop/01-basic-sequential`. Provide an input **`quantity`** (e.g. `1`) when running.
- `06` is the **realistic replacement**: the whole body is supplied via an input **`order`**
  (e.g. `{ "product_id": "ignored", "quantity": 2 }`), and a replacement patches `/product_id` with a
  real catalog id — so the `order` input doesn't even need a valid product id.
- `07` uses the **cart flow** with a **JSONPath replacement target** (`$.product_id`,
  `targetSelectorType: jsonpath`) instead of the default JSON Pointer. Provide an input **`quantity`**.

> **Replacement targets:** the **default** target type is **JSON Pointer** (scenarios 04–06 use
> `/...`). Set `targetSelectorType: jsonpath` to use a **JSONPath** target (scenario 07) — supported now.
> **XPath** targets are **not yet** supported (they log a clear "not yet supported" warning).

### 05 vs 06 — why two replacement examples?
`05` is **contrived** on purpose: the payload is only a `PLACEHOLDER` so you can see the replacement
machinery in isolation — in real life you'd just write that field inline (like `04`). `06` shows where
replacements are *actually* the right tool: the payload arrives **as a whole object you didn't type
out** (here from `$inputs.order`), and you only need to surgically patch one field in it.

## What "working" looks like

- Every step's `successCriteria` (`$statusCode == 200`) passes.
- The **selector outputs hold real extracted values** (a brand id/name, a list of names, a product id) —
  i.e. the runner *evaluated* the Selector Object instead of returning the literal `{context, selector, type}` object.
- For `01`, the plain-string output and the JSON-Pointer selector output should be **the same value**
  (both grab the first brand's id) — a handy sanity check.

## Already verified

The selector strings in these files were checked against toolshop-shaped data (each one extracts the
expected value, incl. the JSONPath-on-root-array cases), and all five files parse and resolve+load
their OpenAPI source. The live API calls themselves are what you'll confirm by running them.
