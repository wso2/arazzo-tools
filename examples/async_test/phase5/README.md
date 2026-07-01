# Phase 5 — Runtime Expression Upgrade examples

These workflows exercise the **Phase 5** evaluator additions: new runtime-expression roots
(`$self`, `$sourceDescriptions`, `$components`, `$workflows`, `$message`) and **compound boolean
success criteria** (`&&`, `||`, `!`, parentheses). Most run against the live **Toolshop** REST API
(`api.practicesoftwaretesting.com`), so **internet is required**.

## The scenarios

| File | What it shows | How to verify |
|---|---|---|
| `01-compound-criteria.arazzo.yaml` | `&&`, `||`, `!`, parentheses in `successCriteria` | all steps pass → workflow succeeds |
| `02-self-expression.arazzo.yaml` | `$self` resolves to the document's `$self` field | output `documentUri` = the `$self` value |
| `03-sourcedescriptions-expression.arazzo.yaml` | `$sourceDescriptions.<name>.<ref>` with §5.9.2 priority | outputs `matchedOperationId`, `operationSummary`, `sourceUrl`, `sourceType` |
| `04-components-expression.arazzo.yaml` | `$components.<type>.<name>` | outputs `configuredLimit` = "10", `paramName` = "limit" |
| `05-workflows-expression.arazzo.yaml` | `$workflows.<id>.<field>` (run **mainFlow**) | output `helperSummary` = helperFlow's summary |
| `06-embedded-serialization.arazzo.yaml` | `{$...}` template: objects→JSON, primitives→text | inspect the addItem request body (`debug_order` is JSON) |
| `07-message-expression.arazzo.yaml` | `$message.header.*` / `$message.payload#/…` (AsyncAPI) | ⚠️ illustrative — see note below |

## Expected output values

- **02** `documentUri` → `./02-self-expression.arazzo.yaml`
- **03** `matchedOperationId` → `getBrands` · `operationSummary` → `Retrieve all brands` ·
  `sourceUrl` → `./toolshop-openapi.yaml` · `sourceType` → `openapi`
- **04** `configuredLimit` → `10` · `paramName` → `limit`
- **05** `helperSummary` → `A reusable helper workflow`

## How to run

Open a file in VS Code with the Arazzo extension and run the workflow (under the hood:
`arazzo-designer-cli serve -f <file>`), the same way as the `phase4_selectors` examples.

- `01`–`05` use only **GET /brands** (plus `03` reads the spec) — **no inputs** needed.
  - For `05`, run the **`mainFlow`** workflow (not `helperFlow`).
- `06` uses the **cart flow** (`getProducts` → `createCart` → `addItem`) and needs an input
  **`order`** object, e.g. `{ "product_id": "ignored", "quantity": 2 }`.

## Notes / caveats

- **`02` uses a *relative* `$self` on purpose.** An absolute `$self` (e.g. `https://…`) would make the
  relative `./toolshop-openapi.yaml` source resolve as **remote** (spec §5.5) and fail to load locally.
  A relative `$self` keeps local loading working while still demonstrating the expression.
- **`$sourceDescriptions` in `operationId` / `operationPath`** (targeting which operation a step calls)
  already worked before Phase 5 — that's a separate code path. `03` shows the **general expression** form.
- **`06` embedded templates** are interpolated for **parameter values and request bodies**, *not* for
  plain step/workflow outputs — so the effect is visible in the outgoing request, not an output value.
- **`07` (`$message.*`) is illustrative and does NOT run yet.** The evaluator support exists, but there
  is no AsyncAPI runtime to deliver a message (broker connection = Phases 9–11), so `$message.*`
  resolves to nil today. The unit test `evaluator.TestEvaluate_Message` is the exact, passing proof.
- **`$url` / `$method`** are implemented but resolve from the request context, which isn't surfaced in
  these output-oriented examples — so they aren't shown here.

## Already verified

The compound conditions and every new expression root are covered by unit tests in
`arazzo-designer-cli/internal/evaluator/evaluator_phase5_test.go` (all passing). These example files
let you confirm the same behavior end-to-end against the live API.
