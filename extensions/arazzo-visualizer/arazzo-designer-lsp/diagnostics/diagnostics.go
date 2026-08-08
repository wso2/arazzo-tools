package diagnostics

import (
	"github.com/arazzo/lsp/parser"
	"github.com/arazzo/lsp/utils"
	"github.com/arazzo/lsp/validator"
	"go.lsp.dev/protocol"
)

// DiagnosticsProvider provides diagnostics for Arazzo documents
type DiagnosticsProvider struct {
	validator *validator.Validator
	parser    *parser.Parser
}

// NewDiagnosticsProvider creates a new DiagnosticsProvider
func NewDiagnosticsProvider() *DiagnosticsProvider {
	return &DiagnosticsProvider{
		validator: validator.NewValidator(),
		parser:    parser.NewParser(),
	}
}

// SetStepActionResolver supplies the hook that tells the validator which direction (send/receive) an
// `operationId`/`operationPath` step targets, resolved from the indexed AsyncAPI sources. The server
// sets this once it can resolve a document's sources; without it the direction-dependent async checks
// only apply to steps that write `action:` themselves.
func (d *DiagnosticsProvider) SetStepActionResolver(fn func(step *parser.Step) (string, bool)) {
	d.validator.WithStepActionResolver(fn)
}

// ProvideDiagnostics generates diagnostics for the given content
func (d *DiagnosticsProvider) ProvideDiagnostics(content string) []protocol.Diagnostic {
	diagnostics := []protocol.Diagnostic{}

	utils.LogDebug("DiagnosticsProvider: Parsing document (length: %d bytes)", len(content))

	// Parse the document
	doc, err := d.parser.Parse(content)
	if err != nil {
		utils.LogError("DiagnosticsProvider: Parse failed: %v", err)
		// Return parse error as diagnostic
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    utils.NewRange(0, 0, 0, 0),
			Severity: protocol.DiagnosticSeverityError,
			Source:   "arazzo-lsp",
			Message:  "Failed to parse document: " + err.Error(),
		})
		return diagnostics
	}

	utils.LogDebug("DiagnosticsProvider: Parse successful, validating document")
	utils.LogDebug("  - Arazzo version: %s", doc.Arazzo)
	utils.LogDebug("  - Info.Title: %s", doc.Info.Title)
	utils.LogDebug("  - Info.Version: %s", doc.Info.Version)
	utils.LogDebug("  - Workflows count: %d", len(doc.Workflows))

	// Validate the document
	validationErrors := d.validator.Validate(doc)
	// Warn about unknown/misspelled fields the struct-based parser silently ignores
	validationErrors = append(validationErrors, d.validator.ValidateUnknownFields(content)...)
	utils.LogDebug("DiagnosticsProvider: Validation completed, found %d errors", len(validationErrors))

	// Convert validation errors to LSP diagnostics
	for _, validationErr := range validationErrors {
		severity := protocol.DiagnosticSeverityError
		if validationErr.Severity == "warning" {
			severity = protocol.DiagnosticSeverityWarning
		}

		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range: utils.NewRange(
				validationErr.Line,
				validationErr.Column,
				validationErr.Line,
				100, // End of line
			),
			Severity: severity,
			Source:   "arazzo-lsp",
			Message:  validationErr.Message,
		})
	}

	return diagnostics
}

// stringPtr returns a pointer to a string
func stringPtr(s string) *string {
	return &s
}
