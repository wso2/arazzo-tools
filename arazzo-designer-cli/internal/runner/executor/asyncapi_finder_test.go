package executor

import "testing"

func asyncSources() map[string]interface{} {
	spec := map[string]interface{}{
		"asyncapi": "3.0.0",
		"channels": map[string]interface{}{
			"orders":        map[string]interface{}{"address": "orders/new"},
			"confirmations": map[string]interface{}{"address": "orders/confirmed"},
		},
		"operations": map[string]interface{}{
			"placeOrder": map[string]interface{}{
				"action":  "send",
				"channel": map[string]interface{}{"$ref": "#/channels/orders"},
			},
			"onConfirmed": map[string]interface{}{
				"action":  "receive",
				"channel": map[string]interface{}{"$ref": "#/channels/confirmations"},
			},
		},
	}
	return map[string]interface{}{"orderBus": spec}
}

func TestAsyncFinder_ChannelByPath(t *testing.T) {
	af := NewAsyncFinder(asyncSources())

	info := af.FindChannelByPath("orderBus#/channels/orders")
	if info == nil {
		t.Fatal("expected to resolve orderBus#/channels/orders")
	}
	if info.Source != "orderBus" || info.ChannelKey != "orders" || info.ChannelAddress != "orders/new" {
		t.Errorf("got source=%q key=%q address=%q, want orderBus/orders/orders/new", info.Source, info.ChannelKey, info.ChannelAddress)
	}

	// braced $sourceDescriptions form for the source ref
	if info := af.FindChannelByPath("{$sourceDescriptions.orderBus.url}#/channels/confirmations"); info == nil || info.ChannelKey != "confirmations" {
		t.Errorf("braced source ref: got %v", info)
	}

	// unknown channel / malformed
	if af.FindChannelByPath("orderBus#/channels/ghost") != nil {
		t.Error("unknown channel should resolve to nil")
	}
	if af.FindChannelByPath("no-hash-here") != nil {
		t.Error("missing '#' should resolve to nil")
	}
}

func TestAsyncFinder_OperationByID(t *testing.T) {
	af := NewAsyncFinder(asyncSources())

	// bare operationId
	info := af.FindOperationByID("placeOrder")
	if info == nil {
		t.Fatal("expected to resolve operationId placeOrder")
	}
	if info.Action != "send" || info.ChannelKey != "orders" || info.ChannelAddress != "orders/new" {
		t.Errorf("got action=%q channel=%q address=%q, want send/orders/orders/new", info.Action, info.ChannelKey, info.ChannelAddress)
	}

	// scoped operationId
	if info := af.FindOperationByID("$sourceDescriptions.orderBus.onConfirmed"); info == nil || info.Action != "receive" || info.ChannelKey != "confirmations" {
		t.Errorf("scoped op: got %v", info)
	}

	// unknown
	if af.FindOperationByID("doesNotExist") != nil {
		t.Error("unknown operationId should resolve to nil")
	}
}

func TestAsyncInfo_ActionMismatch(t *testing.T) {
	af := NewAsyncFinder(asyncSources())
	info := af.FindOperationByID("placeOrder") // action: send

	// step says receive, operation is send -> mismatch, operation action (send) wins
	if opAction, mismatch := info.ActionMismatch("receive"); !mismatch || opAction != "send" {
		t.Errorf("expected mismatch with opAction=send, got %q / %v", opAction, mismatch)
	}
	// step agrees -> no mismatch
	if _, mismatch := info.ActionMismatch("send"); mismatch {
		t.Error("matching actions should not report a mismatch")
	}
	// nothing to compare -> no mismatch
	if _, mismatch := info.ActionMismatch(""); mismatch {
		t.Error("empty step action should not report a mismatch")
	}
}
