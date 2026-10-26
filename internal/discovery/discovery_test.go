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
		"command_topic": "mushi/relay/ch1/set",
		"state_topic": "mushi/relay/ch1/state",
		"payload_on": "ON",
		"payload_off": "OFF"
	}`)

	reg.HandleDiscoveryMessage(prefix, topic, payload)

	if reg.Count() != 1 {
		t.Fatalf("expected 1 registered device, got %d", reg.Count())
	}
	if notifiedTopic != "mushi/relay/ch1/state" {
		t.Fatalf("expected state topic notification, got %s", notifiedTopic)
	}

	dev, ok := reg.GetByID("relay_ch1")
	if !ok || dev.Name != "Channel 1" {
		t.Fatalf("failed to retrieve device")
	}

	// Update state via state topic
	reg.UpdateStateByTopic("mushi/relay/ch1/state", "ON")
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
