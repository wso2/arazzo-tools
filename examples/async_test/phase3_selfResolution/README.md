# Phase 3 — `$self` / source-resolution check examples

These workflows all do the **same simple thing** (list brands, then fetch one brand) against the
real public **Toolshop API** (`api.practicesoftwaretesting.com`). The *only* difference between
them is **how the OpenAPI source is located** — which is exactly what Phase 3 (`$self` / base-URI
resolution) controls.

> All four call the live API, so **internet is required** to run them end to end.

## The four scenarios

| File | `$self` | source `url` | Resolves the OpenAPI from | Proves |
|---|---|---|---|---|
| `01-no-self-local.arazzo.yaml` | *(none)* | `./toolshop-openapi.yaml` | this folder | no `$self` → base is the file's folder (v1.0.x behavior preserved) |
| `02-relative-self-subdir.arazzo.yaml` | `specs/order-flow.arazzo.yaml` (relative) | `./api-spec.yaml` | `./specs/api-spec.yaml` | a **relative** `$self` shifts the base folder |
| `03-absolute-remote-self.arazzo.yaml` | `https://api.practicesoftwaretesting.com/workflows/orders.arazzo.yaml` (absolute) | `../docs` | `https://api.practicesoftwaretesting.com/docs` (remote) | an **absolute** `$self` turns a relative source into a **remote** fetch |
| `04-absolute-remote-source.arazzo.yaml` | *(none)* | `https://api.practicesoftwaretesting.com/docs` | that URL (remote) | an absolute source URL is fetched as-is (pre-v1.1.0 case still works) |

`api-spec.yaml` exists **only** inside `./specs/`, so scenario 2 is a real test: if `$self`
resolution were broken, the source wouldn't be found and the run would fail to load it.

## How to run

Run them the **same way you run `examples/go-runner-test/toolshop`** — open a file in VS Code with
the Arazzo extension and run the workflow. Under the hood that launches the CLI runner:

```
arazzo-designer-cli serve -f 01-no-self-local.arazzo.yaml
```

A successful run loads the OpenAPI source (this is the Phase 3 part) and then executes the two
steps against the live API, returning `firstBrandId` and `verifiedName` in the outputs.

## What "it works" looks like

- **Source loads without error** — this is the Phase 3 behavior. If `$self` resolution were wrong,
  you'd get a *"could not find source file"* (local) or a failed fetch (remote) before any step runs.
- **Both steps succeed** (`$statusCode == 200`) and the workflow outputs a real brand id and name.

## Already verified

The two **local** scenarios (`01`, `02`) have been checked end to end against the real loader —
both resolve and load the toolshop OpenAPI correctly (scenario 2 loads from `./specs`, confirming
the relative-`$self` base shift). The **remote** scenarios (`03`, `04`) depend only on internet
access; their address math is covered by the loader's unit tests (`internal/loader/resolve_test.go`).
