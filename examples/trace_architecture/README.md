# Trace architecture — what a run looks like as spans

Four small examples showing how one run maps onto a trace. All use the **Toolshop API**
(`api.practicesoftwaretesting.com`). **Internet required. No inputs needed.**

## The rules

- **One run is one trace.** Run a workflow twice and you get two traces.
- **A step's child is whatever the step did** — an HTTP request, an async message, or a whole workflow.
- **A nested workflow is a child of the calling step.** That step waits for it, so it contains it.
- **A `dependsOn` dependency is a child of the workflow that needed it.** That workflow waits too, and
  a failed dependency fails it.
- **A `goto` to another workflow is a second root in the same trace.** It hands over instead of
  waiting: the first workflow has already ended, so the second is not inside it.

## The examples

| file | run this workflow | shows |
|---|---|---|
| `01-normal` | `normalRun` | the plain shape: workflow → step → http |
| `02-nested` | `parentRun` | a nested workflow, inside the step that called it |
| `03-dependson` | `mainRun` | a dependency, inside the workflow that needed it, before its steps |
| `04-goto` | `handOverRun` | a handover: same trace, second root |

## What they emit (actual output, not an illustration)

```
01-normal        normalRun          1 trace
  workflow normalRun                  <- root
    step     listBrands
      http     GET /brands
    step     listProducts
      http     GET /products

02-nested        parentRun          1 trace
  workflow parentRun                  <- root
    step     listBrands
      http     GET /brands
    step     callChild                 <- stays open while the child runs
      workflow childRun
        step     listProducts
          http     GET /products

03-dependson     mainRun            1 trace
  workflow mainRun                    <- root
    workflow prepRun                   <- the dependency, and it runs first
      step     listBrands
        http     GET /brands
    step     listProducts              <- mainRun's own steps, after it
      http     GET /products

04-goto          handOverRun        1 trace
  workflow handOverRun                <- root 1, ends when the goto fires
    step     listBrands
      http     GET /brands
  workflow continuedRun               <- root 2, same trace, no parent
    step     listProducts
      http     GET /products
```

An async step would show a `message` span where `http` is above — same position, different transport.
A retried step adds a `retry` span under the workflow.

## Seeing it in a trace viewer

The runner sends spans to any OTLP/HTTP backend. With Jaeger:

```bash
docker run --rm -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one
```

```bash
arazzo-designer-cli serve -f examples/trace_architecture/02-nested.arazzo.yaml -p 8080 --otlp-endpoint http://localhost:4318
```

```bash
curl -X POST http://localhost:8080/run/parentRun
```

Then open <http://localhost:16686> and pick the `arazzo` service. The extension's own Logs tab shows
each span's detail but not the tree — the nesting is what a trace viewer adds.
