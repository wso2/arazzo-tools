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

	// Extract the reference at the cursor. A step targets something in exactly one of three ways:
	// operationId (bare or scoped), channelPath, or operationPath.
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

	// Resolve the document's declared sources to file URIs, and make sure each is parsed/indexed.
	sources := s.ensureSourcesIndexed(uri, content)

	switch {
	// AsyncAPI channelPath -> channel definition, in the NAMED source only.
	case channelPath != "":
		if ch, found := s.lookupChannelInSources(sources, channelPath); found {
			utils.LogInfo("Found channel in %s at line %d", ch.FileName, ch.LineNumber)
			return []protocol.Location{locationAt(ch.FileURI, ch.LineNumber)}, nil
		}
		utils.LogDebug("Channel %q not found in this document's sources", channelPath)

	// operationPath -> the operation the JSON Pointer addresses, in the NAMED source only.
	case operationPath != "":
		if op, found := s.lookupOperationByPath(sources, operationPath); found {
			utils.LogInfo("Found operation via operationPath in %s at line %d", op.FileName, op.LineNumber)
			return []protocol.Location{locationAt(op.FileURI, op.LineNumber)}, nil
		}
		utils.LogDebug("operationPath %q did not resolve in this document's sources", operationPath)

	// operationId (bare or scoped) -> operation definition within this document's declared sources.
	default:
		if op, found := s.lookupOperationInSources(sources, operationID); found {
			utils.LogInfo("Found operation %q in %s at line %d", operationID, op.FileName, op.LineNumber)
			return []protocol.Location{locationAt(op.FileURI, op.LineNumber)}, nil
		}
		utils.LogDebug("Operation %q not found in this document's sources", operationID)
	}
	return nil, nil
}

// ensureSourcesIndexed resolves the document's declared sources and indexes any that aren't yet, so
// a lookup immediately after can find their operations/channels.
func (s *Server) ensureSourcesIndexed(uri protocol.DocumentURI, content string) []resolvedSource {
	// Same per-document lock indexDeclaredSources takes: resolving, indexing and recording the
	// resolved types is a multi-step update of shared state, and a Definition/Hover request must not
	// interleave with a background indexing pass for the same document.
	lock := s.sourceRegistry.lockForIndexing(uri)
	lock.Lock()
	defer lock.Unlock()

	sources := s.resolveDocSources(uri, content)
	for _, src := range sources {
		if !s.operationIndex.HasFile(src.fileURI) {
			if err := s.indexer.IndexFile(src.fileURI); err != nil {
				utils.LogDebug("Could not index source %s (%s): %v", src.name, src.fileURI, err)
				continue
			}
			if specType, ok := s.operationIndex.FileSpecType(src.fileURI); ok {
				s.sourceRegistry.setResolvedType(uri, src.fileURI, specType)
			}
		}
	}
	return sources
}

// lookupChannelInSources resolves a `channelPath` value to its channel definition, scoped to the
// source description it names. Accepts both the bare-name and runtime-expression source forms.
func (s *Server) lookupChannelInSources(sources []resolvedSource, channelPath string) (*navigation.ChannelInfo, bool) {
	srcName, channelKey := splitChannelPath(channelPath)
	if channelKey == "" {
		return nil, false
	}
	fileURI := sourceFileURI(sources, srcName)
	if fileURI == "" {
		utils.LogDebug("channelPath source %q is not a declared source description", srcName)
		return nil, false
	}
	return s.operationIndex.LookupChannelInFile(fileURI, channelKey)
}

// lookupOperationByPath resolves an `operationPath` value ("<sourceRef>#<jsonPointer>") to the
// operation the pointer addresses, scoped to the source description it names. Shared by Definition
// and Hover so the two always agree.
func (s *Server) lookupOperationByPath(sources []resolvedSource, operationPath string) (*navigation.OperationInfo, bool) {
	srcName, pointer, ok := utils.SplitSourceRefAndPointer(operationPath)
	if !ok {
		return nil, false
	}
	fileURI := sourceFileURI(sources, srcName)
	if fileURI == "" {
		utils.LogDebug("operationPath source %q is not a declared source description", srcName)
		return nil, false
	}
	return s.operationIndex.LookupOperationByPointerInFile(fileURI, utils.SplitJSONPointer(pointer))
}

// lookupOperationInSources resolves an operationId to its OperationInfo, scoped to the given declared
// sources. A scoped "$sourceDescriptions.<name>.<op>" resolves in that ONE source; a bare operationId
// is searched across the document's declared sources (first match wins). Shared by Definition and
// Hover so both agree on the same resolution.
func (s *Server) lookupOperationInSources(sources []resolvedSource, operationID string) (*navigation.OperationInfo, bool) {
	if srcName, opID, scoped := utils.ParseScopedOperationID(operationID); scoped {
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
// repeatedly — IndexFile is cache-backed and keyed by file URI. After each file is indexed the type
// it actually turned out to be is recorded on the document's source registry entry.
func (s *Server) indexDeclaredSources(arazzoURI protocol.DocumentURI, content string) {
	// One indexing pass per document at a time: DidOpen/DidChange/DidSave all index in the
	// background, and interleaving two passes could pair one pass's source list with another's
	// resolved types.
	lock := s.sourceRegistry.lockForIndexing(arazzoURI)
	lock.Lock()
	defer lock.Unlock()

	for _, src := range s.resolveDocSources(arazzoURI, content) {
		if err := s.indexer.IndexFile(src.fileURI); err != nil {
			utils.LogDebug("Could not index declared source %s (%s): %v", src.name, src.fileURI, err)
			continue
		}
		if specType, ok := s.operationIndex.FileSpecType(src.fileURI); ok {
			s.sourceRegistry.setResolvedType(arazzoURI, src.fileURI, specType)
		}
	}
}

// resolveDocSources returns the document's declared sources that live on disk, as needed by
// navigation and hover. It refreshes the per-document source registry as a side effect, so the
// registry always reflects the content the editor last handed us.
func (s *Server) resolveDocSources(arazzoURI protocol.DocumentURI, content string) []resolvedSource {
	all := s.refreshDocumentSources(arazzoURI, content)
	out := make([]resolvedSource, 0, len(all))
	for _, ds := range all {
		if ds.Remote || ds.FileURI == "" {
			continue // remote sources can't be navigated to
		}
		out = append(out, resolvedSource{name: ds.Name, typ: ds.EffectiveType(), fileURI: ds.FileURI})
	}
	return out
}

// refreshDocumentSources parses the Arazzo document, resolves each sourceDescription's url to an
// on-disk file URI, and stores the result in the per-document registry. Relative URLs resolve
// against the document's base URI, derived from the optional `$self` field (spec §5.5) — the SAME
// rule the runner uses, so navigation and execution agree on which file a source means.
func (s *Server) refreshDocumentSources(arazzoURI protocol.DocumentURI, content string) []DocumentSource {
	doc, err := parser.NewParser().Parse(content)
	if err != nil || doc == nil {
		return nil
	}
	arazzoPath, err := utils.URIToPath(string(arazzoURI))
	if err != nil {
		return nil
	}
	baseURI := resolveBaseURI(doc.Self, arazzoPath)

	sources := make([]DocumentSource, 0, len(doc.SourceDescriptions))
	for _, sd := range doc.SourceDescriptions {
		ds := DocumentSource{Name: sd.Name, URL: sd.URL, DeclaredType: sd.Type}
		if sd.URL != "" {
			target, remote := resolveSourceLocation(baseURI, sd.URL)
			ds.Remote = remote
			if !remote {
				ds.FileURI = utils.PathToURI(target)
			}
		}
		// Keep a previously detected type across refreshes, then refresh it if the file is indexed.
		if prev, ok := s.sourceRegistry.lookup(arazzoURI, sd.Name); ok && prev.FileURI == ds.FileURI {
			ds.ResolvedType = prev.ResolvedType
		}
		if ds.FileURI != "" {
			if specType, ok := s.operationIndex.FileSpecType(ds.FileURI); ok {
				ds.ResolvedType = specType
			}
		}
		sources = append(sources, ds)
	}
	s.sourceRegistry.set(arazzoURI, sources)
	return sources
}

// resolveStepAsyncAction returns the direction ("send"/"receive") declared by the AsyncAPI operation
// a step targets, for a step that does not write `action:` itself. It is the fact the validator
// cannot obtain on its own — the validator only sees the Arazzo text, while the direction lives in
// the AsyncAPI document.
//
// It reuses the same resolution navigation uses: the step's `operationId` (bare or scoped) or
// `operationPath` is resolved against the document's declared sources, and the indexed operation
// carries its action in Method ("SEND"/"RECEIVE" for AsyncAPI operations; OpenAPI operations carry an
// HTTP verb there instead, which is how they are told apart).
func (s *Server) resolveStepAsyncAction(uri protocol.DocumentURI, content string, step *parser.Step) (string, bool) {
	if step == nil {
		return "", false
	}
	// ensureSourcesIndexed, not resolveDocSources: the lookups below read the operation index, and
	// diagnostics run concurrently with the background indexing pass DidOpen/DidChange start — so on a
	// fresh open the index is usually still empty here and every check that needs it would silently
	// stay quiet. Indexing on demand (cache-backed, same call Definition/Hover make) makes these
	// checks deterministic rather than dependent on which goroutine wins.
	sources := s.ensureSourcesIndexed(uri, content)
	if len(sources) == 0 {
		return "", false
	}

	var op *navigation.OperationInfo
	var found bool
	switch {
	case step.OperationID != "":
		op, found = s.lookupOperationInSources(sources, step.OperationID)
	case step.OperationPath != "":
		op, found = s.lookupOperationByPath(sources, step.OperationPath)
	default:
		return "", false
	}
	if !found || op == nil {
		return "", false
	}

	switch strings.ToLower(op.Method) {
	case "send":
		return "send", true
	case "receive":
		return "receive", true
	default:
		return "", false // an OpenAPI operation: an HTTP verb, not an AsyncAPI direction
	}
}

// resolveStepMessageContentType returns the content type the AsyncAPI document declares for the
// channel a step targets, and whether that channel was resolved at all.
//
// The two return values answer different questions, and the validator needs both:
//   - resolved=false — the target could not be reached (undeclared source, unindexed file, an OpenAPI
//     operation). Nothing can be said, so the content-type diagnostics stay quiet.
//   - resolved=true with an empty slice — the channel WAS found and declares no content type, which
//     is precisely when the runtime silently falls back to JSON. More than one entry means the channel
//     carries messages of different formats and the document cannot say which one a step sends.
//
// It reaches the channel the same way navigation does: a `channelPath` addresses one directly, while
// an `operationId`/`operationPath` resolves to an operation whose indexed ChannelKey points at it.
func (s *Server) resolveStepMessageContentType(uri protocol.DocumentURI, content string, step *parser.Step) (declared []string, resolved bool) {
	if step == nil {
		return nil, false
	}
	sources := s.ensureSourcesIndexed(uri, content) // see resolveStepAsyncAction on why this indexes
	if len(sources) == 0 {
		return nil, false
	}

	if step.ChannelPath != "" {
		ch, found := s.lookupChannelInSources(sources, step.ChannelPath)
		if !found || ch == nil {
			return nil, false
		}
		return ch.ContentTypes, true
	}

	var op *navigation.OperationInfo
	var found bool
	switch {
	case step.OperationID != "":
		op, found = s.lookupOperationInSources(sources, step.OperationID)
	case step.OperationPath != "":
		op, found = s.lookupOperationByPath(sources, step.OperationPath)
	default:
		return nil, false
	}
	if !found || op == nil || op.ChannelKey == "" {
		return nil, false // no channel reference: an OpenAPI operation, or an unresolvable one
	}
	ch, ok := s.operationIndex.LookupChannelInFile(op.FileURI, op.ChannelKey)
	if !ok || ch == nil {
		return nil, false
	}
	return ch.ContentTypes, true
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
// name and the channel key (the last JSON-pointer token). The source ref may be a bare name or the
// spec's "{$sourceDescriptions.<name>.url}" expression — both normalize to the same name (see
// utils.NormalizeSourceRef). Returns ("","") when the value isn't "<ref>#<pointer>" shaped.
func splitChannelPath(value string) (sourceName, channelKey string) {
	sourceName, pointer, ok := utils.SplitSourceRefAndPointer(value)
	if !ok {
		return "", ""
	}
	tokens := utils.SplitJSONPointer(pointer)
	if len(tokens) == 0 {
		return "", ""
	}
	return sourceName, strings.Trim(tokens[len(tokens)-1], `"',`)
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
