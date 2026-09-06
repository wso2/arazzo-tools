// adapter_select.go chooses WHICH transport adapter an async step runs on (Phase 11). Arazzo has no
// broker field — the AsyncAPI document does: its `servers` section declares `protocol` (mqtt, ws, …)
// and `host`. This maps that declaration to an Adapter: ws/wss → WSAdapter, mqtt/mqtts → MQTTAdapter,
// no servers → the default in-memory adapter (tests/local runs). Adapters are cached per
// protocol+host so every step against the same broker shares one connection.
//
// TODO(phase-future): Kafka adapter — implement KafkaAdapter (map channel -> topic, consumer groups,
// auth/TLS) together with REAL Avro/Protobuf codecs + schema-registry config replacing the Phase-10
// schemaRequiredSerializer stubs in serializer.go (Kafka is where those formats are actually used).
package executor

import (
	"fmt"
	"sort"
	"strings"
)

// adapterFor picks the transport for a resolved async target from its source's AsyncAPI `servers`
// declaration. With no servers section the default adapter (in-memory) is used, keeping Phase 9/10
// documents and tests working unchanged.
func (se *StepExecutor) adapterFor(info *AsyncInfo) (Adapter, error) {
	protocol, host := firstServer(toMap(se.SourceDescriptions[info.Source]))
	if protocol == "" {
		return se.AsyncAdapter, nil
	}

	key := protocol + "://" + host
	if a, ok := se.asyncAdapters[key]; ok {
		return a, nil
	}

	var adapter Adapter
	switch strings.ToLower(protocol) {
	case "ws":
		adapter = NewWSAdapter("ws://" + host)
	case "wss":
		adapter = NewWSAdapter("wss://" + host)
	case "mqtt", "mqtts", "secure-mqtt":
		adapter = NewMQTTAdapter(protocol, host)
	case "kafka", "kafka-secure":
		// TODO(phase-future): Kafka adapter + real Avro/Protobuf serializers (see file comment).
		return nil, fmt.Errorf("the %q protocol is not yet supported: a Kafka adapter (with Avro/Protobuf schema support) is a planned future phase - supported protocols: ws, wss, mqtt, mqtts (and in-memory when no servers are declared)", protocol)
	default:
		return nil, fmt.Errorf("unsupported AsyncAPI server protocol %q - supported: ws, wss, mqtt, mqtts (and in-memory when no servers are declared)", protocol)
	}

	if se.asyncAdapters == nil {
		se.asyncAdapters = map[string]Adapter{}
	}
	se.asyncAdapters[key] = adapter
	return adapter, nil
}

// firstServer returns the protocol and host of the AsyncAPI document's first server (by sorted server
// name, so the choice is deterministic — Go map iteration is not). Empty strings mean "no usable
// servers declared".
func firstServer(spec map[string]interface{}) (protocol, host string) {
	servers := toMap(spec["servers"])
	if len(servers) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		server := toMap(servers[name])
		p, _ := server["protocol"].(string)
		h, _ := server["host"].(string)
		if strings.TrimSpace(p) != "" && strings.TrimSpace(h) != "" {
			return strings.TrimSpace(p), strings.TrimSpace(h)
		}
	}
	return "", ""
}
