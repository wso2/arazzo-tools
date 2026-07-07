// resolve.go implements Arazzo v1.1.0 base-URI determination and reference resolution
// (spec §5.5, RFC3986 §5.1). It is used to resolve relative sourceDescriptions URLs against
// the document's base URI, which is derived from the optional `$self` field.
package loader

import (
	"net/url"
	"path/filepath"
	"strings"
)

// IsRemoteURL reports whether s is an absolute http(s) URL.
func IsRemoteURL(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

// ResolveBaseURI determines the base URI of an Arazzo document (spec §5.5; RFC3986 §5.1.1–5.1.4).
// retrievalPath is the location the document was loaded from (a local file path or a URL).
//
//   - $self absent   → the retrieval URI is the base URI (preserves v1.0.x behavior).
//   - $self absolute → $self is the base URI directly.
//   - $self relative → $self is first resolved against the retrieval URI, and the result is the base.
func ResolveBaseURI(self, retrievalPath string) string {
	if self == "" {
		return retrievalPath
	}
	// Per RFC3986 §5.1 a base URI excludes any fragment, and the v1.1.0 spec forbids a fragment in
	// $self (§5.8.1.1). Strip it defensively so headless CLI runs behave spec-correctly even when
	// the LSP validation that flags this to the author hasn't run.
	if i := strings.Index(self, "#"); i >= 0 {
		self = self[:i]
	}
	if self == "" {
		return retrievalPath
	}
	if IsRemoteURL(self) || filepath.IsAbs(self) {
		return self
	}
	// $self is a relative URI-reference: resolve it against the retrieval URI first.
	if IsRemoteURL(retrievalPath) {
		if resolved, ok := resolveURLRef(retrievalPath, self); ok {
			return resolved
		}
		return retrievalPath
	}
	//this means that the arazzo is a local file
	return filepath.Join(filepath.Dir(retrievalPath), self)
}

// ResolveSourceLocation resolves a (possibly relative) sourceDescriptions.url against the base URI.
// It returns the concrete location to load and whether that location is remote (http/https).
// An absolute source URL (remote or absolute local path) always wins over the base URI.
func ResolveSourceLocation(baseURI, sourceURL string) (target string, isRemote bool) {
	if IsRemoteURL(sourceURL) {
		return sourceURL, true
	}
	if filepath.IsAbs(sourceURL) {
		return sourceURL, false
	}
	//this means the source is relative
	if IsRemoteURL(baseURI) {
		if resolved, ok := resolveURLRef(baseURI, sourceURL); ok {
			return resolved, true
		}
		// Remote base but the reference could not be resolved: stay on the remote path. A remote
		// base URI must never yield a local filesystem path, so we must NOT fall through to the
		// filepath.Join below (which would make the caller switch to the local-load path).
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
