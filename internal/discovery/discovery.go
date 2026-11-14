package discovery

import (
	"encoding/json"
	"fmt"
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
	ValueTemplate     string    `json:"val_tpl,omitempty"`
	JSONKey           string    `json:"json_key,omitempty"` // Extracted JSON key e.g. "fan", "temp"
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
	DeviceArea        string    `json:"device_area,omitempty"` // Suggested Area / Zone e.g. Chickenhouse
	LastUpdated       time.Time `json:"last_updated"`
	Source            string    `json:"source"`             // "discovery" or "static"
}

// TopicMessage represents a single historical MQTT message on a custom topic
type TopicMessage struct {
	Payload   string    `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// CustomTopicEntry stores recent messages (up to 3) and parsed JSON fields for arbitrary MQTT topics
type CustomTopicEntry struct {
	Topic       string                 `json:"topic"`
	LastPayload string                 `json:"last_payload"`
	LastJSON    map[string]interface{} `json:"last_json,omitempty"`
	History     []TopicMessage         `json:"history"` // Index 0 is newest, max 3 items
	LastUpdated time.Time              `json:"last_updated"`
}

// Registry manages the in-memory pool of active devices, entity groups, and custom topic cache
type Registry struct {
	mu           sync.RWMutex
	devices      map[string]*DeviceEntity
	topicToIDs   map[string][]string          // state_topic -> list of device IDs (supports multi-channel sharing)
	customTopics map[string]*CustomTopicEntry // arbitrary MQTT topic -> cached messages & JSON
	onNewTopic   func(stateTopic string)
	onStateEvent func(entity *DeviceEntity)
}

// NewRegistry initializes an empty device registry
func NewRegistry(onNewTopic func(stateTopic string), onStateEvent func(entity *DeviceEntity)) *Registry {
	return &Registry{
		devices:      make(map[string]*DeviceEntity),
		topicToIDs:   make(map[string][]string),
		customTopics: make(map[string]*CustomTopicEntry),
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

func extractJSONKey(tpl string) string {
	tpl = strings.TrimSpace(tpl)
	if strings.Contains(tpl, "value_json.") {
		parts := strings.Split(tpl, "value_json.")
		if len(parts) > 1 {
			k := parts[1]
			k = strings.TrimRight(k, " }|")
			k = strings.TrimSpace(k)
			return k
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

	// 4. Payloads & Value Templates
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
	valTpl := getString(raw, "val_tpl", "value_template")
	jsonKey := extractJSONKey(valTpl)

	// 5. Parent Device Grouping Information (supporting 'dev' and 'device')
	deviceID := ""
	deviceName := ""
	deviceModel := ""
	deviceManufacturer := ""
	deviceFirmware := ""
	deviceArea := ""

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
		deviceArea = getString(devObj, "sa", "suggested_area", "area", "zone")

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
	entity.ValueTemplate = valTpl
	entity.JSONKey = jsonKey
	entity.PayloadOn = payloadOn
	entity.PayloadOff = payloadOff
	entity.PayloadPress = payloadPress
	entity.UnitOfMeasurement = unit
	entity.DeviceID = deviceID
	entity.DeviceName = deviceName
	entity.DeviceModel = deviceModel
	entity.Manufacturer = deviceManufacturer
	entity.Firmware = deviceFirmware
	entity.DeviceArea = deviceArea

	isNewTopic := false
	if stateTopic != "" {
		ids := r.topicToIDs[stateTopic]
		found := false
		for _, existingID := range ids {
			if existingID == id {
				found = true
				break
			}
		}
		if !found {
			if len(ids) == 0 {
				isNewTopic = true
			}
			r.topicToIDs[stateTopic] = append(ids, id)
		}
	}

	entityCopy := *entity
	r.mu.Unlock()

	// Execute callbacks outside lock to prevent deadlocks
	if isNewTopic && r.onNewTopic != nil {
		r.onNewTopic(stateTopic)
	}

	if r.onStateEvent != nil {
		r.onStateEvent(&entityCopy)
	}
}

// RegisterStatic adds manually configured devices from config.json
func (r *Registry) RegisterStatic(id, name, component, cmdTopic, stateTopic, pOn, pOff, pPress string) {
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

	r.mu.Lock()
	r.devices[id] = entity
	isNewTopic := false
	if stateTopic != "" {
		ids := r.topicToIDs[stateTopic]
		if len(ids) == 0 {
			isNewTopic = true
		}
		r.topicToIDs[stateTopic] = append(ids, id)
	}
	r.mu.Unlock()

	if isNewTopic && r.onNewTopic != nil {
		r.onNewTopic(stateTopic)
	}
}

// Unregister deletes a device from registry
func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if ent, exists := r.devices[id]; exists {
		if ent.StateTopic != "" {
			ids := r.topicToIDs[ent.StateTopic]
			newIDs := make([]string, 0, len(ids))
			for _, itemID := range ids {
				if itemID != id {
					newIDs = append(newIDs, itemID)
				}
			}
			if len(newIDs) == 0 {
				delete(r.topicToIDs, ent.StateTopic)
			} else {
				r.topicToIDs[ent.StateTopic] = newIDs
			}
		}
		delete(r.devices, id)
	}
}

// UpdateStateByTopic updates device states and custom topic history when a payload arrives
func (r *Registry) UpdateStateByTopic(topic, payload string) []*DeviceEntity {
	trimmed := strings.TrimSpace(payload)
	var jsonDoc map[string]interface{}
	isJSON := false
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		if err := json.Unmarshal([]byte(trimmed), &jsonDoc); err == nil {
			isJSON = true
		}
	}

	now := time.Now()

	r.mu.Lock()
	// 1. Update customTopics cache ONLY if this topic was registered in customTopics
	if ct, ctExists := r.customTopics[topic]; ctExists {
		ct.LastPayload = trimmed
		ct.LastUpdated = now
		if isJSON {
			ct.LastJSON = jsonDoc
		} else {
			ct.LastJSON = nil
		}
		if trimmed != "" {
			// Prepend newest message, keep max 3
			newHist := make([]TopicMessage, 0, 3)
			newHist = append(newHist, TopicMessage{Payload: trimmed, Timestamp: now})
			for _, old := range ct.History {
				if len(newHist) < 3 {
					newHist = append(newHist, old)
				}
			}
			ct.History = newHist
		}
	}

	// 2. Update registered HA / Static entities if any match this topic
	ids, exists := r.topicToIDs[topic]
	if !exists || len(ids) == 0 {
		r.mu.Unlock()
		return nil
	}

	var updated []*DeviceEntity
	for _, id := range ids {
		entity, found := r.devices[id]
		if !found {
			continue
		}

		targetVal := trimmed
		if isJSON {
			if entity.JSONKey != "" {
				if val, ok := jsonDoc[entity.JSONKey]; ok && val != nil {
					targetVal = fmt.Sprintf("%v", val)
				}
			} else {
				if s, ok := jsonDoc["state"].(string); ok {
					targetVal = s
				} else if s, ok := jsonDoc["status"].(string); ok {
					targetVal = s
				} else if s, ok := jsonDoc["value"].(string); ok {
					targetVal = s
				}
			}
		}

		entity.State = targetVal
		entity.LastUpdated = now
		cpy := *entity
		updated = append(updated, &cpy)
	}
	r.mu.Unlock()

	// Broadcast events outside mutex
	if r.onStateEvent != nil {
		for _, ent := range updated {
			r.onStateEvent(ent)
		}
	}

	return updated
}

// EnsureCustomTopic registers an arbitrary MQTT topic for monitoring and triggers subscription
func (r *Registry) EnsureCustomTopic(topic string) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return
	}
	r.mu.Lock()
	_, exists := r.customTopics[topic]
	if !exists {
		// Cap maximum tracked custom topics to 128 to prevent unbounded RAM growth
		if len(r.customTopics) >= 128 {
			r.mu.Unlock()
			return
		}
		r.customTopics[topic] = &CustomTopicEntry{
			Topic:   topic,
			History: make([]TopicMessage, 0, 3),
		}
	}
	r.mu.Unlock()

	if r.onNewTopic != nil {
		r.onNewTopic(topic)
	}
}

// GetCustomTopic returns a copy of a tracked custom topic entry
func (r *Registry) GetCustomTopic(topic string) (CustomTopicEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ct, ok := r.customTopics[topic]
	if !ok {
		return CustomTopicEntry{}, false
	}
	cpy := *ct
	cpy.History = append([]TopicMessage(nil), ct.History...)
	return cpy, true
}

// GetAllCustomTopics returns all tracked custom topics
func (r *Registry) GetAllCustomTopics() []CustomTopicEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make([]CustomTopicEntry, 0, len(r.customTopics))
	for _, ct := range r.customTopics {
		cpy := *ct
		cpy.History = append([]TopicMessage(nil), ct.History...)
		res = append(res, cpy)
	}
	return res
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
