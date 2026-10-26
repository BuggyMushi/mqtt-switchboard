package discovery

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// DeviceEntity represents a single entity/switch/sensor under an IoT device
type DeviceEntity struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Component         string    `json:"component"` // switch, button, sensor, binary_sensor, light, cover
	CommandTopic      string    `json:"cmd_topic,omitempty"`
	StateTopic        string    `json:"state_topic,omitempty"`
	PayloadOn         string    `json:"payload_on,omitempty"`
	PayloadOff        string    `json:"payload_off,omitempty"`
	PayloadPress      string    `json:"payload_press,omitempty"`
	UnitOfMeasurement string    `json:"unit,omitempty"`
	State             string    `json:"state"`
	DeviceID          string    `json:"device_id"`          // Identifier for multi-channel grouping
	DeviceName        string    `json:"device_name"`        // Friendly name of parent device
	DeviceModel       string    `json:"device_model"`       // Hardware model
	Manufacturer      string    `json:"manufacturer"`       // Vendor / Manufacturer e.g. Mushi, Tuya
	Firmware          string    `json:"firmware"`           // Device firmware version e.g. 2.4.0-2.0.0
	LastUpdated       time.Time `json:"last_updated"`
	Source            string    `json:"source"`             // "discovery" or "static"
}

// Registry manages the in-memory pool of active devices and entity groups
type Registry struct {
	mu           sync.RWMutex
	devices      map[string]*DeviceEntity
	topicToID    map[string]string // state_topic -> device ID mapping
	onNewTopic   func(stateTopic string)
	onStateEvent func(entity *DeviceEntity)
}

// NewRegistry initializes an empty device registry
func NewRegistry(onNewTopic func(stateTopic string), onStateEvent func(entity *DeviceEntity)) *Registry {
	return &Registry{
		devices:      make(map[string]*DeviceEntity),
		topicToID:    make(map[string]string),
		onNewTopic:   onNewTopic,
		onStateEvent: onStateEvent,
	}
}

// ParseDiscoveryTopic parses HA discovery topic structure:
// <prefix>/<component>/[<node_id>/]<object_id>/config
func ParseDiscoveryTopic(prefix, topic string) (component, objectID string, valid bool) {
	if !strings.HasPrefix(topic, prefix+"/") || !strings.HasSuffix(topic, "/config") {
		return "", "", false
	}
	trimmed := strings.TrimPrefix(topic, prefix+"/")
	trimmed = strings.TrimSuffix(trimmed, "/config")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		return "", "", false
	}

	component = parts[0]
	objectID = parts[len(parts)-1]
	return component, objectID, true
}

func getString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if val, ok := m[k]; ok && val != nil {
			if s, isStr := val.(string); isStr {
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// HandleDiscoveryMessage processes incoming HA discovery configs, supporting HA abbreviations
func (r *Registry) HandleDiscoveryMessage(prefix, topic string, payload []byte) {
	component, objectID, valid := ParseDiscoveryTopic(prefix, topic)
	if !valid {
		return
	}

	// Empty payload indicates device unregistration in HA discovery protocol
	if len(strings.TrimSpace(string(payload))) == 0 {
		r.Unregister(objectID)
		return
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return
	}

	// 1. Unique ID (supporting standard 'uniq_id' abbreviation and 'unique_id')
	id := getString(raw, "uniq_id", "unique_id")
	if id == "" {
		id = getString(raw, "obj_id", "object_id")
	}
	if id == "" {
		id = objectID
	}

	// 2. Name
	name := getString(raw, "name")
	if name == "" {
		name = id
	}

	// 3. Topics (supporting both HA abbreviations like stat_t/cmd_t and full names)
	stateTopic := getString(raw, "stat_t", "state_topic")
	commandTopic := getString(raw, "cmd_t", "command_topic")

	// 4. Payloads
	payloadOn := getString(raw, "pl_on", "payload_on")
	if payloadOn == "" {
		payloadOn = "ON"
	}
	payloadOff := getString(raw, "pl_off", "payload_off")
	if payloadOff == "" {
		payloadOff = "OFF"
	}
	payloadPress := getString(raw, "pl_prs", "payload_press")
	if payloadPress == "" {
		payloadPress = "PRESS"
	}
	unit := getString(raw, "unit_of_meas", "unit_of_measurement")

	// 5. Parent Device Grouping Information (supporting 'dev' and 'device')
	deviceID := ""
	deviceName := ""
	deviceModel := ""
	deviceManufacturer := ""
	deviceFirmware := ""

	var devObj map[string]interface{}
	if devRaw, ok := raw["dev"]; ok {
		if d, ok := devRaw.(map[string]interface{}); ok {
			devObj = d
		}
	} else if devRaw, ok := raw["device"]; ok {
		if d, ok := devRaw.(map[string]interface{}); ok {
			devObj = d
		}
	}

	if devObj != nil {
		deviceName = getString(devObj, "name")
		deviceModel = getString(devObj, "mdl", "model")
		deviceManufacturer = getString(devObj, "mf", "manufacturer")
		deviceFirmware = getString(devObj, "sw", "sw_version")

		// Extract IDs
		if idsRaw, ok := devObj["ids"]; ok {
			if idsArr, ok := idsRaw.([]interface{}); ok && len(idsArr) > 0 {
				if s, ok := idsArr[0].(string); ok {
					deviceID = s
				}
			} else if s, ok := idsRaw.(string); ok {
				deviceID = s
			}
		} else if idsRaw, ok := devObj["identifiers"]; ok {
			if idsArr, ok := idsRaw.([]interface{}); ok && len(idsArr) > 0 {
				if s, ok := idsArr[0].(string); ok {
					deviceID = s
				}
			} else if s, ok := idsRaw.(string); ok {
				deviceID = s
			}
		}
	}

	if deviceID == "" && deviceName != "" {
		deviceID = deviceName
	}
	if deviceID == "" {
		deviceID = "standalone"
		deviceName = "獨立裝置"
	} else if deviceName == "" {
		deviceName = deviceID
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entity, exists := r.devices[id]
	if !exists {
		entity = &DeviceEntity{
			ID:          id,
			State:       "UNKNOWN",
			Source:      "discovery",
			LastUpdated: time.Now(),
		}
		r.devices[id] = entity
	}

	entity.Name = name
	entity.Component = component
	entity.CommandTopic = commandTopic
	entity.StateTopic = stateTopic
	entity.PayloadOn = payloadOn
	entity.PayloadOff = payloadOff
	entity.PayloadPress = payloadPress
	entity.UnitOfMeasurement = unit
	entity.DeviceID = deviceID
	entity.DeviceName = deviceName
	entity.DeviceModel = deviceModel
	entity.Manufacturer = deviceManufacturer
	entity.Firmware = deviceFirmware

	if stateTopic != "" {
		r.topicToID[stateTopic] = id
		if r.onNewTopic != nil {
			r.onNewTopic(stateTopic)
		}
	}

	if r.onStateEvent != nil {
		r.onStateEvent(entity)
	}
}

// RegisterStatic adds manually configured devices from config.json
func (r *Registry) RegisterStatic(id, name, component, cmdTopic, stateTopic, pOn, pOff, pPress string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if pOn == "" {
		pOn = "ON"
	}
	if pOff == "" {
		pOff = "OFF"
	}
	if pPress == "" {
		pPress = "PRESS"
	}

	entity := &DeviceEntity{
		ID:           id,
		Name:         name,
		Component:    component,
		CommandTopic: cmdTopic,
		StateTopic:   stateTopic,
		PayloadOn:    pOn,
		PayloadOff:   pOff,
		PayloadPress: pPress,
		State:        "UNKNOWN",
		DeviceID:     "static",
		DeviceName:   "自訂靜態設備",
		Source:       "static",
		LastUpdated:  time.Now(),
	}

	r.devices[id] = entity
	if stateTopic != "" {
		r.topicToID[stateTopic] = id
		if r.onNewTopic != nil {
			r.onNewTopic(stateTopic)
		}
	}
}

// Unregister deletes a device from registry
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if ent, exists := r.devices[id]; exists {
		if ent.StateTopic != "" {
			delete(r.topicToID, ent.StateTopic)
		}
		delete(r.devices, id)
	}
}

// UpdateStateByTopic updates device state when a state topic payload arrives
func (r *Registry) UpdateStateByTopic(topic, payload string) *DeviceEntity {
	r.mu.Lock()
	defer r.mu.Unlock()

	id, exists := r.topicToID[topic]
	if !exists {
		return nil
	}

	entity, found := r.devices[id]
	if !found {
		return nil
	}

	trimmed := strings.TrimSpace(payload)
	// If payload is JSON (e.g. {"state":"ON"} or {"rssi":-60}), attempt clean value extraction
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		var jsonDoc map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &jsonDoc); err == nil {
			if s, ok := jsonDoc["state"].(string); ok {
				trimmed = s
			} else if s, ok := jsonDoc["status"].(string); ok {
				trimmed = s
			} else if s, ok := jsonDoc["value"].(string); ok {
				trimmed = s
			}
		}
	}

	entity.State = trimmed
	entity.LastUpdated = time.Now()

	if r.onStateEvent != nil {
		r.onStateEvent(entity)
	}

	return entity
}

// GetAll returns slice of all registered entities
func (r *Registry) GetAll() []DeviceEntity {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]DeviceEntity, 0, len(r.devices))
	for _, item := range r.devices {
		list = append(list, *item)
	}
	return list
}

// GetByID finds device by ID
func (r *Registry) GetByID(id string) (*DeviceEntity, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	item, exists := r.devices[id]
	if !exists {
		return nil, false
	}
	cpy := *item
	return &cpy, true
}

// Count returns number of active devices
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.devices)
}
