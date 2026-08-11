// asyncapi_finder.go resolves AsyncAPI (3.x) channels and operations referenced by v1.1.0 Arazzo
// steps — via `channelPath` (source#<json-pointer-to-channel>) or an AsyncAPI `operationId`
// (bare or scoped `$sourceDescriptions.<name>.<operationId>`). This is Phase 8 "model resolution":
// it lets the tooling UNDERSTAND an async target (channel, address, action, message). It does NOT
// send or receive anything — that is the Phase 9+ adapter runtime.
package executor

import (
	"sort"
	"strings"

	"github.com/wso2/arazzo-designer-cli/internal/evaluator"
)

// AsyncInfo describes a resolved AsyncAPI target for a step.
type AsyncInfo struct {
	Source         string                 // source description name (e.g. "orderBus")
	ChannelKey     string                 // the channel's key under `channels:` (e.g. "orders")
	ChannelAddress string                 // the channel's `address` (broker-side name, e.g. "orders/new")
	Channel        map[string]interface{} // the channel object
	OperationID    string                 // the operation key (when resolved via operationId)
	Operation      map[string]interface{} // the operation object (when resolved via operationId)
	Action         string                 // the operation's declared action ("send"/"receive"), if known

	// DefaultContentType is the AsyncAPI document's root `defaultContentType`. AsyncAPI 3.0 makes it
	// the fallback for a message that omits its own contentType ("When omitted, the value MUST be the
	// one specified on the defaultContentType field"), so message-level resolution needs it.
	DefaultContentType string
}

// Wired into execution as of Phase 9: async_executor.go resolves each async step through
// FindChannelByPath / FindOperationByID, enforces that a `channelPath` step declares an `action`, and
// prefers the AsyncAPI operation's action (warning) when a step contradicts it.

// AsyncFinder resolves AsyncAPI channels/operations in the loaded source descriptions.
type AsyncFinder struct {
	SourceDescriptions map[string]interface{}
}

// NewAsyncFinder creates an AsyncFinder over the loaded source descriptions.
func NewAsyncFinder(sourceDescs map[string]interface{}) *AsyncFinder {
	return &AsyncFinder{SourceDescriptions: sourceDescs}
}

// FindChannelByPath resolves a step `channelPath` of the form "<sourceRef>#<jsonPointer>" (spec
// §5.8.5), e.g. "orderBus#/channels/orders". The part before '#' names the source description (a
// bare name or a "$sourceDescriptions.<name>.url" expression); the JSON Pointer after '#' must point
// at a channel object.
func (af *AsyncFinder) FindChannelByPath(channelPath string) *AsyncInfo {
	hash := strings.Index(channelPath, "#")
	if hash < 0 {
		return nil
	}
	sourceRef := resolveSourceDescriptionRef(strings.Trim(channelPath[:hash], "{}"))
	pointer := channelPath[hash+1:]
	if strings.TrimSpace(pointer) == "" {
		return nil // an empty fragment would resolve to the whole document, not a channel
	}

	name, spec := af.findSource(sourceRef)
	if spec == nil {
		return nil
	}
	channel := toMap(evaluator.ResolveJSONPointer(spec, pointer))
	if channel == nil {
		return nil
	}
	info := &AsyncInfo{
		Source:             name,
		Channel:            channel,
		ChannelKey:         lastPointerSegment(pointer),
		DefaultContentType: docDefaultContentType(spec),
	}
	if addr, ok := channel["address"].(string); ok {
		info.ChannelAddress = addr
	}
	return info
}

// FindOperationByID resolves an AsyncAPI operationId — bare ("placeOrder") or scoped
// ("$sourceDescriptions.<name>.placeOrder") — to its operation object, then follows the operation's
// channel `$ref` to fill in the channel details.
func (af *AsyncFinder) FindOperationByID(operationID string) *AsyncInfo {
	if name, opID, ok := parseQualifiedOperationID(operationID); ok {
		return af.findOperationInSource(name, opID)
	}
	// A bare operationId must resolve to exactly ONE declared source. Ranging over the map directly
	// would pick an arbitrary match (Go randomizes map iteration), so an ambiguous id could resolve
	// differently between runs. Sources are visited in a stable order and an ambiguous id resolves to
	// nothing — the spec already requires the scoped form once several non-arazzo sources exist.
	names := make([]string, 0, len(af.SourceDescriptions))
	for name := range af.SourceDescriptions {
		names = append(names, name)
	}
	sort.Strings(names)

	var found *AsyncInfo
	for _, name := range names {
		info := af.findOperationInSource(name, operationID)
		if info == nil {
			continue
		}
		if found != nil {
			return nil // ambiguous: the same operationId exists in more than one declared source
		}
		found = info
	}
	return found
}

// findOperationInSource looks up an operation by id in one AsyncAPI source (operations are a map
// keyed by operation id in AsyncAPI 3.x) and resolves its channel reference.
func (af *AsyncFinder) findOperationInSource(sourceName, opID string) *AsyncInfo {
	spec := toMap(af.SourceDescriptions[sourceName])
	if spec == nil {
		return nil
	}
	ops := toMap(spec["operations"])
	if ops == nil {
		return nil
	}
	op := toMap(ops[opID])
	if op == nil {
		return nil
	}
	info := &AsyncInfo{Source: sourceName, OperationID: opID, Operation: op, DefaultContentType: docDefaultContentType(spec)}
	if action, ok := op["action"].(string); ok {
		info.Action = action
	}
	// Follow the operation's channel $ref (e.g. "#/channels/orders") to the channel object.
	if ch := toMap(op["channel"]); ch != nil {
		if ref, ok := ch["$ref"].(string); ok && strings.HasPrefix(ref, "#") {
			pointer := strings.TrimPrefix(ref, "#")
			if chObj := toMap(evaluator.ResolveJSONPointer(spec, pointer)); chObj != nil {
				info.Channel = chObj
				info.ChannelKey = lastPointerSegment(pointer)
				if addr, ok := chObj["address"].(string); ok {
					info.ChannelAddress = addr
				}
			}
		}
	}
	return info
}

// docDefaultContentType reads an AsyncAPI document's root `defaultContentType`.
func docDefaultContentType(spec map[string]interface{}) string {
	ct, _ := spec["defaultContentType"].(string)
	return strings.TrimSpace(ct)
}

// DeclaredContentType returns the content type the AsyncAPI document declares for this target, or ""
// when it declares none. It is the "targeted operation" half of the Arazzo rule for a request body's
// contentType: "If omitted then refer to Content-Type specified at the targeted operation to
// understand serialization requirements" (Arazzo §5.8.14.1).
//
// Within the AsyncAPI document the lookup follows that spec's own precedence: a message's own
// `contentType` first, then the document's root `defaultContentType` ("When omitted, the value MUST
// be the one specified on the defaultContentType field", AsyncAPI 3.0 Message Object).
//
// Messages are visited in sorted key order so a channel carrying several message definitions resolves
// the same way on every run (Go randomizes map iteration).
func (info *AsyncInfo) DeclaredContentType() string {
	if info == nil {
		return ""
	}
	messages := toMap(info.Channel["messages"])
	names := make([]string, 0, len(messages))
	for name := range messages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if ct, ok := toMap(messages[name])["contentType"].(string); ok && strings.TrimSpace(ct) != "" {
			return strings.TrimSpace(ct)
		}
	}
	return info.DefaultContentType
}

// ActionMismatch reports whether a step's declared `action` contradicts the resolved operation's
// action. Returns ("", false) when there's nothing to compare (no operation action or no step
// action). When both are present and differ, it returns the operation's action (which WINS per the
// Phase-8/9 decision) and true. Detection only — enforcement/warning happens at the Phase 9 runtime.
func (info *AsyncInfo) ActionMismatch(stepAction string) (operationAction string, mismatch bool) {
	if info == nil || info.Action == "" || stepAction == "" {
		return "", false
	}
	if info.Action != stepAction {
		return info.Action, true
	}
	return "", false
}

// findSource resolves a source name to its spec (exact name match, then a partial-name fallback that
// mirrors OperationFinder.findSourceDescription).
func (af *AsyncFinder) findSource(ref string) (string, map[string]interface{}) {
	if descRaw, ok := af.SourceDescriptions[ref]; ok {
		return ref, toMap(descRaw)
	}
	// Fallback for refs that name a source indirectly (e.g. by url). Visited in a stable order so the
	// result can't vary between runs when more than one name matches (Go randomizes map iteration).
	names := make([]string, 0, len(af.SourceDescriptions))
	for name := range af.SourceDescriptions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.Contains(name, ref) || strings.HasSuffix(ref, name) || strings.Contains(ref, name) {
			return name, toMap(af.SourceDescriptions[name])
		}
	}
	return "", nil
}

// lastPointerSegment returns the final segment of a JSON Pointer ("/channels/orders" -> "orders"),
// decoded per RFC 6901 §3 so an escaped key resolves to its real name ("/channels/orders~1new" ->
// "orders/new"). "~1" must be decoded before "~0" so that "~01" yields "~1", not "/".
func lastPointerSegment(pointer string) string {
	pointer = strings.TrimPrefix(pointer, "/")
	if pointer == "" {
		return ""
	}
	segs := strings.Split(pointer, "/")
	last := segs[len(segs)-1]
	return strings.ReplaceAll(strings.ReplaceAll(last, "~1", "/"), "~0", "~")
}
