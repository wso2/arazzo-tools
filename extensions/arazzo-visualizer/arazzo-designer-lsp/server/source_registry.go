// source_registry.go keeps, PER ARAZZO DOCUMENT, the source descriptions that document declares —
// their name, the type they were declared as, the type the referenced file actually turned out to be,
// and where that file resolved to on disk ($self-aware, spec §5.5).
//
// Why per-document and not global: two Arazzo files may declare different sources under the same
// name, and a name means nothing outside the document that declared it. Keeping this file-level is
// what lets navigation, hover, and validation agree on "which file does `orderEvents` mean HERE",
// and lets a consumer tell which steps target an AsyncAPI (event) source vs an OpenAPI (REST) one.
//
// The registry is refreshed whenever a document is opened, changed, or saved.
package server

import (
	"sort"
	"sync"

	"go.lsp.dev/protocol"
)

// Source description type values as declared in an Arazzo document (spec §4.6.2).
const (
	SourceTypeOpenAPI  = "openapi"
	SourceTypeAsyncAPI = "asyncapi"
	SourceTypeArazzo   = "arazzo"
)

// DocumentSource is one resolved `sourceDescriptions` entry of an Arazzo document.
type DocumentSource struct {
	Name         string `json:"name"`                   // the name steps reference (e.g. "orderEvents")
	URL          string `json:"url"`                    // the raw url exactly as declared
	DeclaredType string `json:"declaredType"`           // `type` as written in the Arazzo document
	ResolvedType string `json:"resolvedType,omitempty"` // what the file actually is, once indexed
	FileURI      string `json:"fileURI,omitempty"`      // resolved local file URI ("" when remote)
	Remote       bool   `json:"remote"`                 // true for http(s) sources (not navigable)
}

// EffectiveType is the source's type for classification purposes: what the file actually turned out
// to be, falling back to what the document declared (the file may be remote or not yet indexed).
func (d DocumentSource) EffectiveType() string {
	if d.ResolvedType != "" {
		return d.ResolvedType
	}
	return d.DeclaredType
}

// IsAsync reports whether this source describes an event-driven (AsyncAPI) API rather than a REST one.
func (d DocumentSource) IsAsync() bool { return d.EffectiveType() == SourceTypeAsyncAPI }

// TypeMismatch reports whether the file's actual type contradicts the declared `type`. Only
// meaningful once the file has been indexed and both types are known.
func (d DocumentSource) TypeMismatch() bool {
	return d.DeclaredType != "" && d.ResolvedType != "" && d.DeclaredType != d.ResolvedType
}

// sourceRegistry maps an Arazzo document URI to the sources it declares.
type sourceRegistry struct {
	mu    sync.RWMutex
	byDoc map[protocol.DocumentURI][]DocumentSource
}

func newSourceRegistry() *sourceRegistry {
	return &sourceRegistry{byDoc: make(map[protocol.DocumentURI][]DocumentSource)}
}

// set replaces everything known about one document's sources.
func (r *sourceRegistry) set(doc protocol.DocumentURI, sources []DocumentSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byDoc[doc] = sources
}

// get returns a copy of a document's sources (safe to read without holding the lock).
func (r *sourceRegistry) get(doc protocol.DocumentURI) []DocumentSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DocumentSource, len(r.byDoc[doc]))
	copy(out, r.byDoc[doc])
	return out
}

// lookup finds one source of a document by name.
func (r *sourceRegistry) lookup(doc protocol.DocumentURI, name string) (DocumentSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.byDoc[doc] {
		if s.Name == name {
			return s, true
		}
	}
	return DocumentSource{}, false
}

// setResolvedType records the type a source's file actually turned out to be, once indexed.
func (r *sourceRegistry) setResolvedType(doc protocol.DocumentURI, fileURI, specType string) {
	if fileURI == "" || specType == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.byDoc[doc] {
		if r.byDoc[doc][i].FileURI == fileURI {
			r.byDoc[doc][i].ResolvedType = specType
		}
	}
}

// remove drops a document's entry (on close).
func (r *sourceRegistry) remove(doc protocol.DocumentURI) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byDoc, doc)
}

// --- server-level accessors ---

// DocumentSources returns the source descriptions declared by an Arazzo document, as last indexed.
func (s *Server) DocumentSources(doc protocol.DocumentURI) []DocumentSource {
	return s.sourceRegistry.get(doc)
}

// AsyncSources returns only the event-driven (AsyncAPI) sources of a document; RESTSources returns
// the rest. Keeping the two apart is what lets a consumer say which kind of API a step talks to.
func (s *Server) AsyncSources(doc protocol.DocumentURI) []DocumentSource {
	return filterSources(s.sourceRegistry.get(doc), true)
}

// RESTSources returns the non-AsyncAPI (OpenAPI/Arazzo) sources of a document.
func (s *Server) RESTSources(doc protocol.DocumentURI) []DocumentSource {
	return filterSources(s.sourceRegistry.get(doc), false)
}

func filterSources(all []DocumentSource, wantAsync bool) []DocumentSource {
	out := make([]DocumentSource, 0, len(all))
	for _, src := range all {
		if src.IsAsync() == wantAsync {
			out = append(out, src)
		}
	}
	return out
}

// SourceInfoResult is the payload of the `arazzo/getSourceInfo` request: a document's declared
// sources, split by kind so a client (e.g. the graph) can tell at a glance which steps target an
// event-driven API and which target a REST one.
type SourceInfoResult struct {
	URI     string           `json:"uri"`
	Sources []DocumentSource `json:"sources"`
	Async   []DocumentSource `json:"async"`
	REST    []DocumentSource `json:"rest"`
}

// buildSourceInfo assembles the response for one document, sorted by name for stable output.
func (s *Server) buildSourceInfo(doc protocol.DocumentURI) *SourceInfoResult {
	all := s.sourceRegistry.get(doc)
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return &SourceInfoResult{
		URI:     string(doc),
		Sources: all,
		Async:   filterSources(all, true),
		REST:    filterSources(all, false),
	}
}
