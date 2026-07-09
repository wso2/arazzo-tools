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

// extractChannelKeyAtPosition returns the channel KEY from a `channelPath:` value at the cursor,
// e.g. "orderBus#/channels/orders" -> "orders". Returns "" if the line isn't a channelPath value.
func extractChannelKeyAtPosition(content string, position protocol.Position) string {
	lines := strings.Split(content, "\n")
	if int(position.Line) >= len(lines) {
		return ""
	}
	line := lines[position.Line]
	if !strings.Contains(line, "channelPath") {
		return ""
	}

	parts := strings.SplitN(line, "channelPath:", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(line, `"channelPath"`, 2)
		if len(parts) < 2 {
			return ""
		}
		afterColon := strings.SplitN(parts[1], ":", 2)
		if len(afterColon) < 2 {
			return ""
		}
		parts[1] = afterColon[1]
	}

	value := strings.TrimSpace(parts[1])
	value = strings.Trim(value, `"'`)

	// value looks like "sourceName#/channels/orders" — take the segment after the last '/'.
	hash := strings.Index(value, "#")
	if hash < 0 {
		return ""
	}
	pointer := value[hash+1:]
	if slash := strings.LastIndex(pointer, "/"); slash != -1 {
		pointer = pointer[slash+1:]
	}
	// strip a trailing comment / quote if any
	pointer = strings.Trim(strings.TrimSpace(pointer), `"',`)
	return pointer
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
