// asyncapi_finder.go resolves AsyncAPI (3.x) channels and operations referenced by v1.1.0 Arazzo
// steps — via `channelPath` (source#<json-pointer-to-channel>) or an AsyncAPI `operationId`
// (bare or scoped `$sourceDescriptions.<name>.<operationId>`). This is Phase 8 "model resolution":
// it lets the tooling UNDERSTAND an async target (channel, address, action, message). It does NOT
// send or receive anything — that is the Phase 9+ adapter runtime.
package executor

import (
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
}

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

	name, spec := af.findSource(sourceRef)
	if spec == nil {
		return nil
	}
	channel := toMap(evaluator.ResolveJSONPointer(spec, pointer))
	if channel == nil {
		return nil
	}
	info := &AsyncInfo{
		Source:     name,
		Channel:    channel,
		ChannelKey: lastPointerSegment(pointer),
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
	for name := range af.SourceDescriptions {
		if info := af.findOperationInSource(name, operationID); info != nil {
			return info
		}
	}
	return nil
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
	info := &AsyncInfo{Source: sourceName, OperationID: opID, Operation: op}
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
	for name, descRaw := range af.SourceDescriptions {
		if strings.Contains(name, ref) || strings.HasSuffix(ref, name) || strings.Contains(ref, name) {
			return name, toMap(descRaw)
		}
	}
	return "", nil
}

// lastPointerSegment returns the final segment of a JSON Pointer ("/channels/orders" -> "orders").
func lastPointerSegment(pointer string) string {
	pointer = strings.TrimPrefix(pointer, "/")
	if pointer == "" {
		return ""
	}
	segs := strings.Split(pointer, "/")
	return segs[len(segs)-1]
}
