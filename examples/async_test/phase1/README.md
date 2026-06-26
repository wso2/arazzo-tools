# Phase 1 Test Examples — Arazzo v1.1.0

This folder contains test examples for Phase 1 of the Arazzo v1.1.0 implementation.

## Files

| File | Purpose |
|---|---|
| `v101-backward-compat.arazzo.yaml` | Verifies that an existing Arazzo v1.0.1 document still parses without errors |
| `v110-openapi-new-fields.arazzo.yaml` | Arazzo v1.1.0 document using new Phase 1 fields with OpenAPI operations (`$self`, new parameter types, step `dependsOn`, widened outputs, `parameters` in actions, new ExpressionTypeObject format) |
| `v110-asyncapi-channel.arazzo.yaml` | Arazzo v1.1.0 document using the new `channelPath`, `action`, `correlationId`, and `timeout` fields targeting an AsyncAPI source |
| `invalid-v110.arazzo.yaml` | Intentionally invalid Arazzo v1.1.0 document; expects validation errors / specific diagnostics from the LSP |

## What Phase 1 validates

- The LSP accepts `arazzo: "1.1.0"` without an error diagnostic
- The LSP accepts `type: asyncapi` in `sourceDescriptions` without an error
- The LSP accepts `channelPath` as a valid step target selector (not an error)
- The LSP accepts the new step-level `dependsOn` field without warnings
- The `arazzo.tmLanguage.json` highlights `channelPath`, `timeout`, `correlationId`, `action`, `dependsOn` as step keywords
- The `$self`, `$message` runtime expressions are highlighted in strings
- All three files should be syntax-highlighted correctly when opened in VS Code with the extension

## Backward compatibility

`v101-backward-compat.arazzo.yaml` uses `arazzo: "1.0.1"` and must continue to:
1. Parse without any errors in the LSP diagnostics panel
2. Activate `arazzo-yaml` language mode in VS Code
3. Show completions and syntax highlighting as before
