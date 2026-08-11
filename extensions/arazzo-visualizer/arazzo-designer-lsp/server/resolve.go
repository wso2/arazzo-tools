// resolve.go replicates the runner's Arazzo v1.1.0 base-URI determination and reference resolution
// (spec §5.5, RFC3986 §5.1). It resolves relative sourceDescriptions URLs against the document's base
// URI, which is derived from the optional `$self` field — so LSP navigation resolves sources exactly
// the way the CLI runner does. It is a standalone copy on purpose: the runner lives in a separate Go
// module (and will move elsewhere), so this must not import it.
package server

import (
	"net/url"
	"path/filepath"
	"strings"
)

// isRemoteURL reports whether s is an absolute http(s) URL.
func isRemoteURL(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

// resolveBaseURI determines the base URI of an Arazzo document (spec §5.5; RFC3986 §5.1.1–5.1.4).
// retrievalPath is the location the document was loaded from (a local file path or a URL).
//
//   - $self absent   → the retrieval URI is the base URI (preserves v1.0.x behavior).
//   - $self absolute → $self is the base URI directly.
//   - $self relative → $self is first resolved against the retrieval URI, and the result is the base.
func resolveBaseURI(self, retrievalPath string) string {
	if self == "" {
		return retrievalPath
	}
	// Per RFC3986 §5.1 a base URI excludes any fragment, and the v1.1.0 spec forbids a fragment in
	// $self (§5.8.1.1). Strip it defensively.
	if i := strings.Index(self, "#"); i >= 0 {
		self = self[:i]
	}
	if self == "" {
		return retrievalPath
	}
	if isRemoteURL(self) || filepath.IsAbs(self) {
		return self
	}
	// $self is a relative URI-reference: resolve it against the retrieval URI first.
	if isRemoteURL(retrievalPath) {
		if resolved, ok := resolveURLRef(retrievalPath, self); ok {
			return resolved
		}
		return retrievalPath
	}
	// The Arazzo document is a local file.
	return filepath.Join(filepath.Dir(retrievalPath), self)
}

// resolveSourceLocation resolves a (possibly relative) sourceDescriptions.url against the base URI.
// It returns the concrete location and whether that location is remote (http/https). An absolute
// source URL (remote or absolute local path) always wins over the base URI.
func resolveSourceLocation(baseURI, sourceURL string) (target string, isRemote bool) {
	if isRemoteURL(sourceURL) {
		return sourceURL, true
	}
	if filepath.IsAbs(sourceURL) {
		return sourceURL, false
	}
	// The source URL is relative.
	if isRemoteURL(baseURI) {
		if resolved, ok := resolveURLRef(baseURI, sourceURL); ok {
			return resolved, true
		}
		// Remote base but the reference could not be resolved: stay remote (never yield a local path).
		return sourceURL, true
	}
	// Local base: resolve relative to the base document's directory.
	return filepath.Join(filepath.Dir(baseURI), sourceURL), false
}

// resolveURLRef resolves a (relative) reference against an absolute base URL per RFC3986.
func resolveURLRef(base, ref string) (string, bool) {
	bu, err := url.Parse(base)
	if err != nil {
		return "", false
	}
	ru, err := url.Parse(ref)
	if err != nil {
		return "", false
	}
	return bu.ResolveReference(ru).String(), true
}
