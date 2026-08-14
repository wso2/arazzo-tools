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

// StepResolvers supplies the facts about a step that live in the SOURCE documents rather than in the
// Arazzo text, resolved from the indexed AsyncAPI/OpenAPI files. Each is optional: a nil resolver
// simply keeps the checks that depend on it quiet.
//
//   - Action — the direction (send/receive) an `operationId`/`operationPath` step targets, which the
//     Arazzo text only states when the step writes `action:` itself.
//   - ContentType — every content type the AsyncAPI document declares for the step's channel, plus
//     whether that channel was resolved at all.
//
// These are PER-CALL values rather than provider state: diagnostics for different documents must never
// share a resolver, or one document's validation could resolve against another's sources.
type StepResolvers struct {
	Action              func(step *parser.Step) (action string, ok bool)
	ContentType         func(step *parser.Step) (declared []string, resolved bool)
	CorrelationLocation func(step *parser.Step) (locations []string, source string, resolved bool)
}

// ProvideDiagnostics generates diagnostics for the given content, using resolvers for the facts that
// live outside the Arazzo document (see StepResolvers).
func (d *DiagnosticsProvider) ProvideDiagnostics(content string, resolvers StepResolvers) []protocol.Diagnostic {
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
	// A validator scoped to THIS call, so the resolver above cannot leak into another document's
	// validation (the shared provider is reused across documents and requests).
	v := validator.NewValidator().
		WithStepActionResolver(resolvers.Action).
		WithStepContentTypeResolver(resolvers.ContentType).
		WithStepCorrelationLocationResolver(resolvers.CorrelationLocation)
	validationErrors := v.Validate(doc)
	// Warn about unknown/misspelled fields the struct-based parser silently ignores
	validationErrors = append(validationErrors, v.ValidateUnknownFields(content)...)
	utils.LogDebug("DiagnosticsProvider: Validation completed, found %d errors", len(validationErrors))

	// Convert validation errors to LSP diagnostics
	for _, validationErr := range validationErrors {
		severity := protocol.DiagnosticSeverityError
		switch validationErr.Severity {
		case "warning":
			severity = protocol.DiagnosticSeverityWarning
		case "information":
			// Not a defect: the document is legal and the runtime has a defined behaviour. These
			// surface a silent assumption (e.g. "this will be serialized as JSON") without marking a
			// correct document as a problem.
			severity = protocol.DiagnosticSeverityInformation
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
