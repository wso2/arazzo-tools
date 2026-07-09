package server

import (
	"context"

	"github.com/arazzo/lsp/utils"
	"go.lsp.dev/protocol"
)

// Definition handles the textDocument/definition request
// Provides "Go to Definition" functionality for operationId references
func (s *Server) Definition(ctx context.Context, params *protocol.DefinitionParams) ([]protocol.Location, error) {
	uri := params.TextDocument.URI
	utils.LogDebug("Definition request for: %s at line %d, char %d", uri, params.Position.Line, params.Position.Character)

	// Get document content
	content, ok := s.documents[uri]
	if !ok {
		utils.LogWarning("Document not found: %s", uri)
		return nil, nil
	}

	// Extract the reference at the cursor: an operationId, or (AsyncAPI) a channelPath.
	operationID := extractOperationIdAtPosition(content, params.Position)
	channelKey := ""
	if operationID == "" {
		channelKey = extractChannelKeyAtPosition(content, params.Position)
	}
	if operationID == "" && channelKey == "" {
		utils.LogDebug("No operationId or channelPath found at position")
		return nil, nil
	}

	// Ensure the index is built (covers OpenAPI operations, AsyncAPI operations, and channels).
	if s.operationIndex == nil || (s.operationIndex.Count() == 0 && s.operationIndex.ChannelCount() == 0) {
		utils.LogWarning("Index is empty, building index...")
		if err := s.indexer.BuildIndex(string(uri)); err != nil {
			utils.LogError("Failed to build index: %v", err)
			return nil, nil
		}
	}

	// AsyncAPI channelPath -> channel definition.
	if operationID == "" {
		utils.LogDebug("Looking up channel: %s", channelKey)
		ch, found := s.operationIndex.LookupChannel(channelKey)
		if !found {
			utils.LogDebug("Channel not found: %s", channelKey)
			return nil, nil
		}
		utils.LogInfo("Found channel: %s in %s at line %d", channelKey, ch.FileName, ch.LineNumber)
		return []protocol.Location{locationAt(ch.FileURI, ch.LineNumber)}, nil
	}

	// operationId (OpenAPI or AsyncAPI) -> operation definition.
	utils.LogDebug("Looking up operationId: %s", operationID)
	opInfo, found := s.operationIndex.Lookup(operationID)
	if !found {
		utils.LogDebug("Operation not found: %s", operationID)
		return nil, nil
	}
	utils.LogInfo("Found operation: %s in %s at line %d", operationID, opInfo.FileName, opInfo.LineNumber)
	return []protocol.Location{locationAt(opInfo.FileURI, opInfo.LineNumber)}, nil
}

// locationAt builds an LSP Location pointing at the start of the given (0-indexed) line.
func locationAt(fileURI string, line int) protocol.Location {
	return protocol.Location{
		URI: protocol.DocumentURI(fileURI),
		Range: protocol.Range{
			Start: protocol.Position{Line: uint32(line), Character: 0},
			End:   protocol.Position{Line: uint32(line), Character: 100},
		},
	}
}
