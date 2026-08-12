// sourceref.go holds the shared parsing rules for the three ways an Arazzo step targets something in
// a source description: `channelPath`, `operationPath` (both "<sourceRef>#<jsonPointer>") and the
// scoped `operationId` form. Navigation, hover, and validation all normalize references through here
// so they can never disagree about which source description a step points at.
package utils

import "strings"

// sourceDescriptionsPrefix is the runtime-expression prefix that identifies a source description
// (Arazzo spec §5.9.2, e.g. "$sourceDescriptions.petstore.url").
const sourceDescriptionsPrefix = "$sourceDescriptions."

// NormalizeSourceRef reduces any way of naming a source description down to its bare name. The spec
// says a Runtime Expression syntax MUST be used to identify the source document in `operationPath`
// and `channelPath` (e.g. "{$sourceDescriptions.petstore.url}#/paths/~1pet/get"), but a plain source
// name is common in the wild and unambiguous, so both are accepted:
//
//	orderEvents                              -> orderEvents
//	{$sourceDescriptions.orderEvents.url}    -> orderEvents
//	$sourceDescriptions.orderEvents.url      -> orderEvents
//	{$sourceDescriptions.orderEvents}        -> orderEvents
//
// Anything after the source name (".url" and friends) is dropped: for these fields the reference
// always identifies the source DOCUMENT, whichever field of it was used to do so.
func NormalizeSourceRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, `"'`)
	ref = strings.TrimSpace(strings.Trim(strings.TrimSpace(ref), "{}"))
	if !strings.HasPrefix(ref, sourceDescriptionsPrefix) {
		return ref
	}
	rest := ref[len(sourceDescriptionsPrefix):]
	if dot := strings.Index(rest, "."); dot >= 0 {
		return rest[:dot]
	}
	return rest
}

// SplitSourceRefAndPointer splits a `channelPath`/`operationPath` value of the form
// "<sourceRef>#<jsonPointer>" into the normalized source-description name and the raw JSON Pointer
// (the fragment after '#', still escaped). ok is false when the value has no '#' or either side is
// empty — i.e. when it doesn't have the shape the spec requires.
func SplitSourceRefAndPointer(value string) (sourceName, pointer string, ok bool) {
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), `"'`))
	hash := strings.Index(value, "#")
	if hash < 0 {
		return "", "", false
	}
	rawRef := strings.TrimSpace(value[:hash])
	pointer = strings.TrimSpace(value[hash+1:])
	if rawRef == "" || pointer == "" {
		return "", "", false
	}
	return NormalizeSourceRef(rawRef), pointer, true
}

// IsRuntimeExpressionSourceRef reports whether the source-reference part of a value uses the runtime
// expression form the spec mandates ("{$sourceDescriptions.<name>.url}"), as opposed to a bare name.
// Used to inform (not fail) authors that the expression form is the spec-preferred spelling.
func IsRuntimeExpressionSourceRef(rawRef string) bool {
	rawRef = strings.TrimSpace(rawRef)
	rawRef = strings.Trim(rawRef, `"'`)
	rawRef = strings.TrimSpace(strings.Trim(strings.TrimSpace(rawRef), "{}"))
	return strings.HasPrefix(rawRef, sourceDescriptionsPrefix)
}

// ParseScopedOperationID splits the scoped operationId form
// "$sourceDescriptions.<name>.<operationId>" into its parts. ok is false for a bare operationId.
// Note this differs from NormalizeSourceRef: here the trailing segment is the operation id and is
// KEPT, whereas for a source-document reference it is discarded.
func ParseScopedOperationID(ref string) (sourceName, operationID string, ok bool) {
	ref = strings.TrimSpace(ref)
	ref = strings.Trim(ref, `"'`)
	ref = strings.TrimSpace(strings.Trim(strings.TrimSpace(ref), "{}"))
	if !strings.HasPrefix(ref, sourceDescriptionsPrefix) {
		return "", "", false
	}
	rest := ref[len(sourceDescriptionsPrefix):]
	dot := strings.Index(rest, ".")
	if dot < 0 || dot == 0 || dot == len(rest)-1 {
		return "", "", false
	}
	return rest[:dot], rest[dot+1:], true
}

// UnescapeJSONPointerToken decodes one JSON Pointer reference token per RFC 6901 §3: "~1" is '/' and
// "~0" is '~'. Order matters — "~1" must be decoded before "~0" so that "~01" yields "~1", not "/".
func UnescapeJSONPointerToken(token string) string {
	return strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
}

// SplitJSONPointer splits a JSON Pointer ("/paths/~1products/get") into its decoded reference tokens
// (["paths", "/products", "get"]). A leading '/' is required by RFC 6901; an empty pointer or "/"
// yields no tokens.
func SplitJSONPointer(pointer string) []string {
	pointer = strings.TrimSpace(pointer)
	if pointer == "" || pointer == "/" {
		return nil
	}
	pointer = strings.TrimPrefix(pointer, "/")
	parts := strings.Split(pointer, "/")
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		tokens = append(tokens, UnescapeJSONPointerToken(p))
	}
	return tokens
}

// SameMediaType reports whether two content types resolve to the same wire format. Parameters are
// dropped and a `+json` structured suffix counts as JSON, so "application/vnd.order+json",
// "application/json" and "application/json; charset=utf-8" all compare equal — the runner's serializer
// registry selects one serializer for all three, so nothing disagrees about the format.
//
// Mirrors sameMediaType in the runner's executor package. The two modules cannot import each other, so
// the rule is stated once per module rather than in each package that needs it.
func SameMediaType(a, b string) bool {
	fold := func(ct string) string {
		ct = strings.TrimSpace(strings.ToLower(ct))
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = strings.TrimSpace(ct[:i])
		}
		if strings.HasSuffix(ct, "+json") {
			return "application/json"
		}
		return ct
	}
	return fold(a) == fold(b)
}
