package server

import (
	"context"
	"strings"

	"github.com/arazzo/lsp/navigation"
	"github.com/arazzo/lsp/parser"
	"github.com/arazzo/lsp/utils"
	"go.lsp.dev/protocol"
)

// resolvedSource is a sourceDescription resolved to an on-disk file URI (local refs only).
type resolvedSource struct {
	name    string
	typ     string // "openapi" | "asyncapi" | "arazzo" (as declared)
	fileURI string
}

// Definition handles the textDocument/definition request — "Go to Definition" for a step's
// operationId (OpenAPI or AsyncAPI) or channelPath (AsyncAPI channel).
//
// Resolution is SCOPED to the source descriptions declared in THIS Arazzo document: a reference is
// looked up only in the files those sources point at (resolved relative to the Arazzo file), never
// across the whole workspace. This prevents an operationId from resolving into an unrelated spec that
// merely happens to be indexed (e.g. a same-named operation in another folder).
func (s *Server) Definition(ctx context.Context, params *protocol.DefinitionParams) ([]protocol.Location, error) {
	uri := params.TextDocument.URI
	utils.LogDebug("Definition request for: %s at line %d, char %d", uri, params.Position.Line, params.Position.Character)

	content, ok := s.documents[uri]
	if !ok {
		utils.LogWarning("Document not found: %s", uri)
		return nil, nil
	}

	// Extract the reference at the cursor: an operationId (bare or scoped), or a channelPath value.
	operationID := extractOperationIdAtPosition(content, params.Position)
	channelPath := ""
	if operationID == "" {
		channelPath = extractChannelPathAtPosition(content, params.Position)
	}
	if operationID == "" && channelPath == "" {
		utils.LogDebug("No operationId or channelPath found at position")
		return nil, nil
	}

	// Resolve the document's declared sources to file URIs, and make sure each is parsed/indexed.
	sources := s.resolveDocSources(uri, content)
	for _, src := range sources {
		if !s.operationIndex.HasFile(src.fileURI) {
			if err := s.indexer.IndexFile(src.fileURI); err != nil {
				utils.LogDebug("Could not index source %s (%s): %v", src.name, src.fileURI, err)
			}
		}
	}

	// AsyncAPI channelPath -> channel definition, in the NAMED source only.
	if operationID == "" {
		srcName, channelKey := splitChannelPath(channelPath)
		if channelKey == "" {
			return nil, nil
		}
		fileURI := sourceFileURI(sources, srcName)
		if fileURI == "" {
			utils.LogDebug("channelPath source %q is not a declared source description", srcName)
			return nil, nil
		}
		if ch, found := s.operationIndex.LookupChannelInFile(fileURI, channelKey); found {
			utils.LogInfo("Found channel %q in %s at line %d", channelKey, ch.FileName, ch.LineNumber)
			return []protocol.Location{locationAt(ch.FileURI, ch.LineNumber)}, nil
		}
		utils.LogDebug("Channel %q not found in source %q", channelKey, srcName)
		return nil, nil
	}

	// operationId (bare or scoped "$sourceDescriptions.<name>.<op>") -> operation definition,
	// resolved only within this document's declared sources.
	if op, found := s.lookupOperationInSources(sources, operationID); found {
		utils.LogInfo("Found operation %q in %s at line %d", operationID, op.FileName, op.LineNumber)
		return []protocol.Location{locationAt(op.FileURI, op.LineNumber)}, nil
	}
	utils.LogDebug("Operation %q not found in this document's sources", operationID)
	return nil, nil
}

// lookupOperationInSources resolves an operationId to its OperationInfo, scoped to the given declared
// sources. A scoped "$sourceDescriptions.<name>.<op>" resolves in that ONE source; a bare operationId
// is searched across the document's declared sources (first match wins). Shared by Definition and
// Hover so both agree on the same resolution.
func (s *Server) lookupOperationInSources(sources []resolvedSource, operationID string) (*navigation.OperationInfo, bool) {
	if srcName, opID, scoped := parseScopedOperationID(operationID); scoped {
		fileURI := sourceFileURI(sources, srcName)
		if fileURI == "" {
			return nil, false
		}
		return s.operationIndex.LookupOperationInFile(fileURI, opID)
	}
	for _, src := range sources {
		if op, found := s.operationIndex.LookupOperationInFile(src.fileURI, operationID); found {
			return op, true
		}
	}
	return nil, false
}

// indexDeclaredSources parses an Arazzo document and indexes ONLY the source-description files it
// declares (resolved relative to the Arazzo file). This is the scoped alternative to a workspace/
// directory scan: a document sees exactly the specs it references and nothing else. Safe to call
// repeatedly — IndexFile is cache-backed and keyed by file URI.
func (s *Server) indexDeclaredSources(arazzoURI protocol.DocumentURI, content string) {
	for _, src := range s.resolveDocSources(arazzoURI, content) {
		if err := s.indexer.IndexFile(src.fileURI); err != nil {
			utils.LogDebug("Could not index declared source %s (%s): %v", src.name, src.fileURI, err)
		}
	}
}

// resolveDocSources parses the Arazzo document and resolves each sourceDescription's url to an
// on-disk file URI. Relative URLs are resolved against the document's base URI, which is derived from
// the optional `$self` field (spec §5.5) — the SAME rule the runner uses, so navigation and execution
// agree. Remote (http/https) refs are skipped: navigation can only jump to a local file.
func (s *Server) resolveDocSources(arazzoURI protocol.DocumentURI, content string) []resolvedSource {
	doc, err := parser.NewParser().Parse(content)
	if err != nil || doc == nil {
		return nil
	}
	arazzoPath, err := utils.URIToPath(string(arazzoURI))
	if err != nil {
		return nil
	}
	baseURI := resolveBaseURI(doc.Self, arazzoPath)

	var out []resolvedSource
	for _, sd := range doc.SourceDescriptions {
		if sd.URL == "" {
			continue
		}
		target, remote := resolveSourceLocation(baseURI, sd.URL)
		if remote {
			continue
		}
		out = append(out, resolvedSource{name: sd.Name, typ: sd.Type, fileURI: utils.PathToURI(target)})
	}
	return out
}

// sourceFileURI returns the resolved file URI for the source description with the given name (or "").
func sourceFileURI(sources []resolvedSource, name string) string {
	for _, s := range sources {
		if s.name == name {
			return s.fileURI
		}
	}
	return ""
}

// splitChannelPath splits a channelPath value "<sourceRef>#<jsonPointer>" into the source-description
// name and the channel key (the last JSON-pointer segment). The source ref may be a bare name or a
// "{$sourceDescriptions.<name>.url}" expression. Returns ("","") if there is no '#'.
func splitChannelPath(value string) (sourceName, channelKey string) {
	hash := strings.Index(value, "#")
	if hash < 0 {
		return "", ""
	}
	sourceName = resolveSourceRefName(strings.Trim(value[:hash], "{}"))
	pointer := value[hash+1:]
	if slash := strings.LastIndex(pointer, "/"); slash != -1 {
		pointer = pointer[slash+1:]
	}
	channelKey = strings.Trim(strings.TrimSpace(pointer), `"',`)
	return sourceName, channelKey
}

// resolveSourceRefName turns a "$sourceDescriptions.<name>.url" expression into "<name>"; a bare name
// is returned unchanged.
func resolveSourceRefName(ref string) string {
	const prefix = "$sourceDescriptions."
	const suffix = ".url"
	if strings.HasPrefix(ref, prefix) && strings.HasSuffix(ref, suffix) {
		return ref[len(prefix) : len(ref)-len(suffix)]
	}
	return ref
}

// parseScopedOperationID splits "$sourceDescriptions.<name>.<operationId>" into its source name and
// operationId. ok is false for a bare operationId (no "$sourceDescriptions." prefix).
func parseScopedOperationID(ref string) (sourceName, operationID string, ok bool) {
	const prefix = "$sourceDescriptions."
	if !strings.HasPrefix(ref, prefix) {
		return "", "", false
	}
	rest := ref[len(prefix):]
	dot := strings.Index(rest, ".")
	if dot < 0 {
		return "", "", false
	}
	return rest[:dot], rest[dot+1:], true
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
