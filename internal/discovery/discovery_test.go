package discovery

import (
	"testing"
)

func TestParseDiscoveryTopic(t *testing.T) {
	prefix := "homeassistant"

	// 3-segment: homeassistant/<component>/<object_id>/config
	comp, obj, ok := ParseDiscoveryTopic(prefix, "homeassistant/switch/relay1/config")
	if !ok || comp != "switch" || obj != "relay1" {
		t.Fatalf("failed 3-segment parse: %v, %s, %s", ok, comp, obj)
	}

	// 4-segment: homeassistant/<component>/<node_id>/<object_id>/config
	comp, obj, ok = ParseDiscoveryTopic(prefix, "homeassistant/sensor/nodeA/temp/config")
	if !ok || comp != "sensor" || obj != "temp" {
		t.Fatalf("failed 4-segment parse: %v, %s, %s", ok, comp, obj)
	}

	// Invalid
	_, _, ok = ParseDiscoveryTopic(prefix, "other/switch/relay1/config")
	if ok {
		t.Fatalf("expected failure on mismatched prefix")
	}
}

func TestRegistry_DiscoveryAndUnregister(t *testing.T) {
	var notifiedTopic string
	var notifiedState string

	reg := NewRegistry(
		func(topic string) { notifiedTopic = topic },
		func(dev *DeviceEntity) { notifiedState = dev.State },
	)

	prefix := "homeassistant"
	topic := "homeassistant/switch/relay_ch1/config"
	payload := []byte(`{
		"name": "Channel 1",
		"unique_id": "relay_ch1",
		"command_topic": "iot/relay/ch1/set",
		"state_topic": "iot/relay/ch1/state",
		"payload_on": "ON",
		"payload_off": "OFF"
	}`)

	reg.HandleDiscoveryMessage(prefix, topic, payload)

	if reg.Count() != 1 {
		t.Fatalf("expected 1 registered device, got %d", reg.Count())
	}
	if notifiedTopic != "iot/relay/ch1/state" {
		t.Fatalf("expected state topic notification, got %s", notifiedTopic)
	}

	dev, ok := reg.GetByID("relay_ch1")
	if !ok || dev.Name != "Channel 1" {
		t.Fatalf("failed to retrieve device")
	}

	// Update state via state topic
	reg.UpdateStateByTopic("iot/relay/ch1/state", "ON")
	dev, _ = reg.GetByID("relay_ch1")
	if dev.State != "ON" || notifiedState != "ON" {
		t.Fatalf("expected state ON, got %s", dev.State)
	}

	// Empty payload -> Unregister
	reg.HandleDiscoveryMessage(prefix, topic, []byte(""))
	if reg.Count() != 0 {
		t.Fatalf("expected device unregister, got %d", reg.Count())
	}
}

func TestRegistry_HAAbbreviationsAndGrouping(t *testing.T) {
	var subscribedTopics []string
	reg := NewRegistry(
		func(topic string) { subscribedTopics = append(subscribedTopics, topic) },
		nil,
	)

	prefix := "homeassistant"
	// Relay CH1 discovery payload using official abbreviations
	topicCH1 := "homeassistant/switch/SmartRelay_FACD_CH1/config"
	payloadCH1 := []byte(`{
		"name": "SmartRelay_CH1",
		"uniq_id": "SmartRelay_FACD_CH1",
		"stat_t": "iot/SmartRelay_FACD/Switch/1/status",
		"cmd_t": "iot/SmartRelay_FACD/Switch/1/set",
		"pl_on": "ON",
		"pl_off": "OFF",
		"dev": {
			"ids": ["SmartRelay_FACD"],
			"name": "SmartRelay_FACD",
			"mdl": "Smart Relay 8CH"
		}
	}`)

	reg.HandleDiscoveryMessage(prefix, topicCH1, payloadCH1)

	dev, ok := reg.GetByID("SmartRelay_FACD_CH1")
	if !ok {
		t.Fatalf("failed to parse CH1 entity with abbreviations")
	}

	if dev.StateTopic != "iot/SmartRelay_FACD/Switch/1/status" {
		t.Fatalf("stat_t abbreviation failed: got %s", dev.StateTopic)
	}
	if dev.CommandTopic != "iot/SmartRelay_FACD/Switch/1/set" {
		t.Fatalf("cmd_t abbreviation failed: got %s", dev.CommandTopic)
	}
	if dev.DeviceID != "SmartRelay_FACD" {
		t.Fatalf("expected DeviceID SmartRelay_FACD, got %s", dev.DeviceID)
	}

	// Update state
	reg.UpdateStateByTopic("iot/SmartRelay_FACD/Switch/1/status", "ON")
	dev, _ = reg.GetByID("SmartRelay_FACD_CH1")
	if dev.State != "ON" {
		t.Fatalf("expected state ON, got %s", dev.State)
	}
}
