package server

import (
	"strings"

	"go.lsp.dev/protocol"
)

// extractOperationIdAtPosition extracts the operationId value at the given position
func extractOperationIdAtPosition(content string, position protocol.Position) string {
	lines := strings.Split(content, "\n")

	// Check if position is valid
	if int(position.Line) >= len(lines) {
		return ""
	}

	line := lines[position.Line]

	// Check if this line contains "operationId"
	if !strings.Contains(line, "operationId") {
		return ""
	}

	// Extract the value after "operationId:"
	// Format can be:
	//   operationId: findPetsByTags
	//   operationId: "findPetsByTags"
	//   operationId: 'findPetsByTags'

	parts := strings.SplitN(line, "operationId:", 2)
	if len(parts) < 2 {
		// Try with operationId" for JSON
		parts = strings.SplitN(line, `"operationId"`, 2)
		if len(parts) < 2 {
			return ""
		}

		// Extract value after colon in JSON: "operationId": "value"
		afterColon := strings.SplitN(parts[1], ":", 2)
		if len(afterColon) < 2 {
			return ""
		}
		parts[1] = afterColon[1]
	}

	// Get the value part
	value := strings.TrimSpace(parts[1])

	// Remove quotes if present
	value = strings.Trim(value, `"'`)

	// Remove trailing comments or commas
	if idx := strings.IndexAny(value, "#,"); idx != -1 {
		value = value[:idx]
	}

	value = strings.TrimSpace(value)

	return value
}

// extractFieldValueAtPosition returns the scalar value of "<field>:" on the cursor's line, unquoted
// and with any trailing inline comment removed. It is the shared basis for the `channelPath` and
// `operationPath` extractors, which have the same "<sourceRef>#<jsonPointer>" shape.
//
// Quoting matters here: the spec's runtime-expression form starts with '{', which YAML would read as
// a flow mapping, so those values are always quoted (e.g. '{$sourceDescriptions.cat.url}#/paths/~1p/get').
// A '#' inside a quoted scalar is part of the value, not a comment — so quotes are handled first.
func extractFieldValueAtPosition(content string, position protocol.Position, field string) string {
	lines := strings.Split(content, "\n")
	if int(position.Line) >= len(lines) {
		return ""
	}
	line := lines[position.Line]
	if !strings.Contains(line, field) {
		return ""
	}

	parts := strings.SplitN(line, field+":", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(line, `"`+field+`"`, 2)
		if len(parts) < 2 {
			return ""
		}
		afterColon := strings.SplitN(parts[1], ":", 2)
		if len(afterColon) < 2 {
			return ""
		}
		parts[1] = afterColon[1]
	}

	// The match must be the WHOLE property key, not a substring of a longer one and not text inside
	// a comment: `x-channelPath:` and `# channelPath: …` both contain the field name but are not it.
	// Only YAML indentation and an optional sequence dash may precede the key.
	if !isKeyPrefix(parts[0]) {
		return ""
	}

	value := strings.TrimSpace(parts[1])
	if value == "" {
		return ""
	}

	// Quoted scalar: the value is everything up to the matching closing quote.
	if q := value[0]; q == '"' || q == '\'' {
		if end := strings.IndexByte(value[1:], q); end >= 0 {
			return strings.TrimSpace(value[1 : 1+end])
		}
		return strings.TrimSpace(strings.Trim(value, `"'`))
	}

	// Unquoted scalar: " #" starts a YAML comment (a real reference's '#' has no leading space).
	if idx := strings.Index(value, " #"); idx != -1 {
		value = value[:idx]
	}
	// In a flow mapping the value ends at the next ',' or the closing '}' rather than at end of line.
	// An unquoted value can't legitimately contain either: the runtime-expression source form starts
	// with '{', which YAML would read as a flow mapping, so that form is always quoted and handled above.
	if idx := strings.IndexAny(value, ",}"); idx != -1 {
		value = value[:idx]
	}
	return strings.TrimSpace(value)
}

// NOT USED — no production caller (Definition/Hover use extractChannelPathAtPosition, which keeps
// the source-name part needed to scope the lookup). Kept because it is still covered by a test and
// documents the key-only form; safe to remove together with that test.
//
// isKeyPrefix reports whether a matched field name is the WHOLE property key rather than the tail of
// a longer one or text inside a comment, judged from what precedes it on the line.
//
// The test is the character immediately before the match: a key can only follow whitespace, the
// start of the line, or a flow-mapping punctuator (`{`, `,`) or an opening quote — never an
// identifier character. That rejects `x-channelPath` (preceded by '-') and `mychannelPath`
// (preceded by 'y') while still accepting indentation, `- channelPath:` sequence items, and
// flow-style `{stepId: s, channelPath: …}`. A '#' anywhere before the match means the key is inside
// a comment.
func isKeyPrefix(prefix string) bool {
	if strings.Contains(prefix, "#") {
		return false // the line is commented out at or before this point
	}
	if prefix == "" {
		return true
	}
	switch last := prefix[len(prefix)-1]; {
	case last == ' ', last == '\t', last == '{', last == ',', last == '"', last == '\'':
		return true
	default:
		return false
	}
}

// extractChannelKeyAtPosition returns the channel KEY from a `channelPath:` value at the cursor,
// e.g. "orderBus#/channels/orders" -> "orders". Returns "" if the line isn't a channelPath value.
func extractChannelKeyAtPosition(content string, position protocol.Position) string {
	_, key := splitChannelPath(extractChannelPathAtPosition(content, position))
	return key
}

// extractChannelPathAtPosition returns the FULL `channelPath:` value at the cursor, e.g.
// "orderBus#/channels/orders" (source ref + JSON pointer, unquoted). Returns "" if the line isn't a
// channelPath value. Unlike extractChannelKeyAtPosition it keeps the source-name part, which the
// definition provider needs to scope the lookup to the right source description.
func extractChannelPathAtPosition(content string, position protocol.Position) string {
	return extractFieldValueAtPosition(content, position, "channelPath")
}

// extractOperationPathAtPosition returns the FULL `operationPath:` value at the cursor, e.g.
// "{$sourceDescriptions.catalog.url}#/paths/~1products/get". Returns "" if the line isn't an
// operationPath value.
func extractOperationPathAtPosition(content string, position protocol.Position) string {
	return extractFieldValueAtPosition(content, position, "operationPath")
}

// isOperationIdField checks if the cursor position is on an operationId field
func isOperationIdField(content string, position protocol.Position) bool {
	lines := strings.Split(content, "\n")

	if int(position.Line) >= len(lines) {
		return false
	}

	line := lines[position.Line]
	return strings.Contains(line, "operationId")
}
