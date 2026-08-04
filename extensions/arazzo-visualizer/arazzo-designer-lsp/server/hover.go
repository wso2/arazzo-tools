package server

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/arazzo/lsp/navigation"
	"github.com/arazzo/lsp/utils"
	"go.lsp.dev/protocol"
)

// Hover handles the textDocument/hover request
// Provides operation information on hover over operationId
func (s *Server) Hover(ctx context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	uri := params.TextDocument.URI
	utils.LogDebug("Hover request for: %s at line %d, char %d", uri, params.Position.Line, params.Position.Character)

	// Get document content
	content, ok := s.documents[uri]
	if !ok {
		utils.LogWarning("Document not found: %s", uri)
		return nil, nil
	}

	// Hover covers the same three targeting forms as Go-to-Definition, resolved by the SAME helpers,
	// so the popup can never describe a different file than Ctrl+Click would jump to.
	operationID := extractOperationIdAtPosition(content, params.Position)
	channelPath, operationPath := "", ""
	if operationID == "" {
		channelPath = extractChannelPathAtPosition(content, params.Position)
	}
	if operationID == "" && channelPath == "" {
		operationPath = extractOperationPathAtPosition(content, params.Position)
	}
	if operationID == "" && channelPath == "" && operationPath == "" {
		utils.LogDebug("No operationId, channelPath or operationPath found at position")
		return nil, nil
	}

	// Resolve strictly within this document's declared sources (same as Go-to-Definition) so the
	// popup can't show an operation from an unrelated spec that merely shares the operationId.
	sources := s.ensureSourcesIndexed(uri, content)

	var markdown string
	switch {
	case channelPath != "":
		ch, found := s.lookupChannelInSources(sources, channelPath)
		if !found {
			utils.LogDebug("Channel not found for hover: %s", channelPath)
			return nil, nil
		}
		markdown = buildChannelHoverMarkdown(ch)

	case operationPath != "":
		opInfo, found := s.lookupOperationByPath(sources, operationPath)
		if !found {
			utils.LogDebug("operationPath not resolved for hover: %s", operationPath)
			return nil, nil
		}
		markdown = buildHoverMarkdown(opInfo)

	default:
		opInfo, found := s.lookupOperationInSources(sources, operationID)
		if !found {
			utils.LogDebug("Operation not found for hover in this document's sources: %s", operationID)
			return nil, nil
		}
		markdown = buildHoverMarkdown(opInfo)
	}

	hover := &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown,
		},
		Range: &protocol.Range{
			Start: protocol.Position{
				Line:      params.Position.Line,
				Character: 0,
			},
			End: protocol.Position{
				Line:      params.Position.Line,
				Character: 100,
			},
		},
	}

	return hover, nil
}

// buildHoverMarkdown creates formatted markdown content for hover
func buildHoverMarkdown(opInfo *navigation.OperationInfo) string {
	if opInfo == nil {
		return "**Operation**: Information not available"
	}

	op := opInfo

	var md strings.Builder

	// Header
	md.WriteString(fmt.Sprintf("### %s `%s`\n\n", op.Method, op.OperationID))

	// Path
	if op.Path != "" {
		md.WriteString(fmt.Sprintf("**Path**: `%s`\n\n", op.Path))
	}

	// Summary
	if op.Summary != "" {
		md.WriteString(fmt.Sprintf("**Summary**: %s\n\n", op.Summary))
	}

	// Description
	if op.Description != "" {
		md.WriteString(fmt.Sprintf("%s\n\n", op.Description))
	}

	// File location
	fileName := filepath.Base(op.FileName)
	md.WriteString("---\n\n")
	md.WriteString(fmt.Sprintf("📄 **Defined in**: `%s:%d`\n\n", fileName, op.LineNumber))

	// Action hint
	md.WriteString("*Ctrl+Click to navigate to definition*")

	return md.String()
}

// buildChannelHoverMarkdown creates hover content for an AsyncAPI channel referenced by channelPath.
// It shows the channel KEY (the doc-internal name the pointer addresses) and its ADDRESS (the real
// broker topic) — the distinction that most often trips people up when reading AsyncAPI.
func buildChannelHoverMarkdown(ch *navigation.ChannelInfo) string {
	if ch == nil {
		return "**Channel**: Information not available"
	}

	var md strings.Builder
	md.WriteString(fmt.Sprintf("### channel `%s`\n\n", ch.Key))
	if ch.Address != "" {
		md.WriteString(fmt.Sprintf("**Address**: `%s`\n\n", ch.Address))
	}
	md.WriteString("---\n\n")
	md.WriteString(fmt.Sprintf("📄 **Defined in**: `%s:%d`\n\n", filepath.Base(ch.FileName), ch.LineNumber))
	md.WriteString("*Ctrl+Click to navigate to definition*")

	return md.String()
}

// buildSimpleHoverMarkdown creates hover content when full operation info not available
func buildSimpleHoverMarkdown(operationID, method, path, fileName string, lineNumber int) string {
	var md strings.Builder

	md.WriteString(fmt.Sprintf("### %s `%s`\n\n", method, operationID))

	if path != "" {
		md.WriteString(fmt.Sprintf("**Path**: `%s`\n\n", path))
	}

	if fileName != "" {
		md.WriteString("---\n\n")
		md.WriteString(fmt.Sprintf("📄 **Defined in**: `%s:%d`\n\n", filepath.Base(fileName), lineNumber))
	}

	md.WriteString("*Ctrl+Click to navigate to definition*")

	return md.String()
}
