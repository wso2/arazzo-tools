package navigation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arazzo/lsp/utils"
	"gopkg.in/yaml.v3"
)

// ParseOpenAPIFile parses an OpenAPI specification file and extracts operation information
func ParseOpenAPIFile(fileURI string) (*OpenAPIFile, error) {
	utils.LogDebug("Parsing OpenAPI file: %s", fileURI)

	// Read file content
	filePath, err := utils.URIToPath(fileURI)
	if err != nil {
		utils.LogError("Failed to convert URI to path - URI: '%s', Error: %v", fileURI, err)
		return nil, fmt.Errorf("invalid URI %s: %w", fileURI, err)
	}

	utils.LogDebug("Converted URI to path: '%s' -> '%s'", fileURI, filePath)

	content, err := os.ReadFile(filePath)
	if err != nil {
		utils.LogError("Failed to read file - URI: '%s', Path: '%s', Error: %v", fileURI, filePath, err)
		return nil, fmt.Errorf("failed to read file '%s' (from URI '%s'): %w", filePath, fileURI, err)
	}

	// Parse based on file extension
	var spec map[string]interface{}
	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".json" {
		err := json.Unmarshal(content, &spec)
		if err != nil {
			return nil, fmt.Errorf("failed to parse file: %w", err)
		}
	} else {
		err := yaml.Unmarshal(content, &spec)
		if err != nil {
			return nil, fmt.Errorf("failed to parse file: %w", err)
		}
	}

	// Extract OpenAPI file metadata. SpecType records what the file ACTUALLY is (from its own
	// `openapi:`/`asyncapi:` key), which lets callers compare it against the `type` an Arazzo
	// document declared for this source.
	openAPIFile := &OpenAPIFile{
		URI:        fileURI,
		Version:    getString(spec, "openapi"),
		Operations: make([]*OperationInfo, 0),
	}
	if openAPIFile.Version != "" {
		openAPIFile.SpecType = "openapi"
	}

	// Extract info if present
	if info, ok := spec["info"].(map[string]interface{}); ok {
		openAPIFile.Title = getString(info, "title")
		openAPIFile.Description = getString(info, "description")
	}

	// An AsyncAPI document has no `paths`, so running the OpenAPI extractor over it only produces a
	// misleading "no paths found in OpenAPI spec" warning. Decide the spec type first and run only
	// the extractor that applies.
	if asyncVersion := getString(spec, "asyncapi"); asyncVersion != "" {
		openAPIFile.Version = asyncVersion
		openAPIFile.SpecType = "asyncapi"
		openAPIFile.Operations = extractAsyncOperations(spec, fileURI, string(content))
		openAPIFile.Channels = extractChannels(spec, fileURI, string(content))
	} else {
		operations, err := extractOperations(spec, fileURI, string(content))
		if err != nil {
			utils.LogWarning("Error extracting operations: %v", err)
			// Continue even if some operations fail to parse
		}
		openAPIFile.Operations = operations
	}

	utils.LogInfo("Parsed %d operations, %d channels from %s", len(openAPIFile.Operations), len(openAPIFile.Channels), filepath.Base(filePath))

	return openAPIFile, nil
}

// extractAsyncOperations extracts AsyncAPI 3.x operations (the `operations` map keyed by id).
func extractAsyncOperations(spec map[string]interface{}, fileURI, content string) []*OperationInfo {
	ops := make([]*OperationInfo, 0)
	operationsObj, ok := spec["operations"].(map[string]interface{})
	if !ok {
		return ops
	}
	fileName := baseName(fileURI)
	for opID, opRaw := range operationsObj {
		opMap, ok := opRaw.(map[string]interface{})
		if !ok {
			continue
		}
		ops = append(ops, &OperationInfo{
			OperationID: opID,
			Method:      strings.ToUpper(getString(opMap, "action")), // SEND / RECEIVE (for display)
			Summary:     getString(opMap, "summary"),
			Description: getString(opMap, "description"),
			FileURI:     fileURI,
			FileName:    fileName,
			LineNumber:  findKeyLineNumber(content, opID),
			Column:      0,
			ChannelKey:  operationChannelKey(opMap),
		})
	}
	return ops
}

// extractChannels extracts AsyncAPI channels (the `channels` map keyed by channel key).
func extractChannels(spec map[string]interface{}, fileURI, content string) []*ChannelInfo {
	channels := make([]*ChannelInfo, 0)
	channelsObj, ok := spec["channels"].(map[string]interface{})
	if !ok {
		return channels
	}
	fileName := baseName(fileURI)
	defaultContentType := getString(spec, "defaultContentType")
	for key, chRaw := range channelsObj {
		chMap, _ := chRaw.(map[string]interface{})
		channels = append(channels, &ChannelInfo{
			Key:         key,
			Address:     getString(chMap, "address"),
			FileURI:     fileURI,
			FileName:    fileName,
			LineNumber:  findKeyLineNumber(content, key),
			ContentTypes:         channelContentTypes(spec, chMap, defaultContentType),
			CorrelationLocations: channelCorrelationLocations(spec, chMap),
		})
	}
	return channels
}

// channelContentTypes resolves the distinct wire formats a channel's messages declare, following
// AsyncAPI 3.0's own precedence: each message's `contentType`, and "When omitted, the value MUST be the
// one specified on the defaultContentType field" (Message Object). A channel's messages may be inline
// or `$ref`s into `components.messages`, so each is dereferenced first. Messages are visited in sorted
// key order so a channel with several message definitions resolves identically on every parse.
//
// Mirrors AsyncInfo.DeclaredContentTypes in the runner, so the editor and the run agree.
func channelContentTypes(spec, channel map[string]interface{}, defaultContentType string) []string {
	messages, _ := channel["messages"].(map[string]interface{})
	names := make([]string, 0, len(messages))
	for name := range messages {
		names = append(names, name)
	}
	sort.Strings(names)

	var types []string
	for _, name := range names {
		msg, _ := messages[name].(map[string]interface{})
		ct := getString(resolveLocalRef(spec, msg), "contentType")
		if ct == "" || containsMediaType(types, ct) {
			continue
		}
		types = append(types, ct)
	}
	if len(types) > 0 {
		return types
	}
	if defaultContentType != "" {
		return []string{defaultContentType}
	}
	return nil
}

// channelCorrelationLocations resolves the distinct places a channel's messages say their correlation
// id lives, from each message's AsyncAPI Correlation ID Object. Mirrors the runtime's
// AsyncInfo.DeclaredCorrelationLocations so the editor reports what a run will actually do.
//
// Both the message and the Correlation ID Object may be `$ref`s into `components`, so each is
// dereferenced first. Locations are compared verbatim — a JSON Pointer expression has no equivalent
// spellings the way a media type does, so two differing strings are two different places.
func channelCorrelationLocations(spec, channel map[string]interface{}) []string {
	messages, _ := channel["messages"].(map[string]interface{})
	names := make([]string, 0, len(messages))
	for name := range messages {
		names = append(names, name)
	}
	sort.Strings(names)

	var locations []string
	for _, name := range names {
		msg, _ := messages[name].(map[string]interface{})
		correlation, _ := resolveLocalRef(spec, msg)["correlationId"].(map[string]interface{})
		location := getString(resolveLocalRef(spec, correlation), "location")
		if location == "" || containsExact(locations, location) {
			continue
		}
		locations = append(locations, location)
	}
	return locations
}

// containsExact reports whether values already holds s, compared verbatim.
func containsExact(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

// containsMediaType reports whether types already holds this wire format, comparing the way the
// runtime's registry keys on it so two spellings of one format count once.
func containsMediaType(types []string, contentType string) bool {
	for _, t := range types {
		if utils.SameMediaType(t, contentType) {
			return true
		}
	}
	return false
}

// resolveLocalRef follows a local "$ref" ("#/components/messages/alert") to the object it names.
// Anything else — no $ref, an external ref, or one that doesn't resolve — is returned unchanged, so a
// caller can always read the object it already has.
func resolveLocalRef(spec, obj map[string]interface{}) map[string]interface{} {
	ref := getString(obj, "$ref")
	if !strings.HasPrefix(ref, "#/") {
		return obj
	}
	var current interface{} = spec
	for _, token := range utils.SplitJSONPointer(strings.TrimPrefix(ref, "#")) {
		m, ok := current.(map[string]interface{})
		if !ok {
			return obj
		}
		if current, ok = m[token]; !ok {
			return obj
		}
	}
	if resolved, ok := current.(map[string]interface{}); ok {
		return resolved
	}
	return obj
}

// operationChannelKey follows an AsyncAPI operation's `channel.$ref` (e.g. "#/channels/orders") to the
// channel key it targets. Returns "" when the operation has no local channel reference.
func operationChannelKey(op map[string]interface{}) string {
	channel, _ := op["channel"].(map[string]interface{})
	ref := getString(channel, "$ref")
	if !strings.HasPrefix(ref, "#/channels/") {
		return ""
	}
	return utils.UnescapeJSONPointerToken(strings.TrimPrefix(ref, "#/channels/"))
}

// findKeyLineNumber finds the (0-indexed) line where a YAML/JSON map key is defined, e.g. the
// `placeOrder:` operation key or the `orders:` channel key.
func findKeyLineNumber(content, key string) int {
	lines := strings.Split(content, "\n")
	yamlKey := key + ":"
	jsonKey := `"` + key + `"`
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, yamlKey) || strings.HasPrefix(trimmed, jsonKey) {
			return i
		}
	}
	return 0
}

// baseName returns the display filename for a URI.
func baseName(fileURI string) string {
	if filePath, err := utils.URIToPath(fileURI); err == nil {
		return filepath.Base(filePath)
	}
	return filepath.Base(fileURI)
}

// extractOperations extracts operation information from the paths object
func extractOperations(spec map[string]interface{}, fileURI, content string) ([]*OperationInfo, error) {
	operations := make([]*OperationInfo, 0)

	// Get paths object
	pathsObj, ok := spec["paths"]
	if !ok {
		return operations, fmt.Errorf("no paths found in OpenAPI spec")
	}

	paths, ok := pathsObj.(map[string]interface{})
	if !ok {
		return operations, fmt.Errorf("paths is not an object")
	}

	// Iterate through each path
	for pathStr, pathItem := range paths {
		pathItemMap, ok := pathItem.(map[string]interface{})
		if !ok {
			continue
		}

		// Iterate through HTTP methods
		for method, operation := range pathItemMap {
			// Skip non-operation fields
			if method == "parameters" || method == "summary" || method == "description" || method == "$ref" {
				continue
			}

			// Check if this is a valid HTTP method
			methodUpper := strings.ToUpper(method)
			if !isHTTPMethod(methodUpper) {
				continue
			}

			operationMap, ok := operation.(map[string]interface{})
			if !ok {
				continue
			}

			// Extract operationId
			operationID := getString(operationMap, "operationId")
			if operationID == "" {
				utils.LogDebug("Skipping operation without operationId: %s %s", methodUpper, pathStr)
				continue
			}

			// Find line number in content
			lineNumber := findLineNumber(content, operationID)

			// Create operation info
			// Extract filename from URI (handle both platforms)
			fileName := filepath.Base(fileURI)
			if filePath, err := utils.URIToPath(fileURI); err == nil {
				fileName = filepath.Base(filePath)
			}

			opInfo := &OperationInfo{
				OperationID: operationID,
				Method:      methodUpper,
				Path:        pathStr,
				Summary:     getString(operationMap, "summary"),
				Description: getString(operationMap, "description"),
				FileURI:     fileURI,
				FileName:    fileName,
				LineNumber:  lineNumber,
				Column:      0,
				Tags:        getStringArray(operationMap, "tags"),
			}

			operations = append(operations, opInfo)
			utils.LogDebug("Found operation: %s %s -> %s (line %d)", methodUpper, pathStr, operationID, lineNumber)
		}
	}

	return operations, nil
}

// findLineNumber finds the line number where an operationId is defined
func findLineNumber(content, operationID string) int {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		// Look for "operationId: <value>" or "operationId": "<value>"
		if strings.Contains(line, "operationId") &&
			(strings.Contains(line, operationID) ||
				strings.Contains(line, fmt.Sprintf(`"%s"`, operationID))) {
			return i // Line numbers are 0-indexed
		}
	}

	return 0 // Default to first line if not found
}

// isHTTPMethod checks if a string is a valid HTTP method
func isHTTPMethod(method string) bool {
	validMethods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "TRACE"}
	for _, valid := range validMethods {
		if method == valid {
			return true
		}
	}
	return false
}

// getString safely extracts a string value from a map
func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// getStringArray safely extracts a string array from a map
func getStringArray(m map[string]interface{}, key string) []string {
	result := make([]string, 0)

	if val, ok := m[key]; ok {
		if arr, ok := val.([]interface{}); ok {
			for _, item := range arr {
				if str, ok := item.(string); ok {
					result = append(result, str)
				}
			}
		}
	}

	return result
}
