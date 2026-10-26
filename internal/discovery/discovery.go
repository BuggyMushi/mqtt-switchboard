package discovery

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// DeviceEntity represents a discovered or statically configured IoT device card
type DeviceEntity struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Component         string    `json:"component"` // switch, button, sensor, binary_sensor, light
	CommandTopic      string    `json:"cmd_topic,omitempty"`
	StateTopic        string    `json:"state_topic,omitempty"`
	PayloadOn         string    `json:"payload_on,omitempty"`
	PayloadOff        string    `json:"payload_off,omitempty"`
	PayloadPress      string    `json:"payload_press,omitempty"`
	UnitOfMeasurement string    `json:"unit,omitempty"`
	State             string    `json:"state"`
	LastUpdated       time.Time `json:"last_updated"`
	Source            string    `json:"source"` // "discovery" or "static"
}

// HADiscoveryPayload models the standard Home Assistant MQTT discovery JSON schema
type HADiscoveryPayload struct {
	Name              string `json:"name"`
	UniqueID          string `json:"unique_id"`
	ObjectID          string `json:"object_id"`
	CommandTopic      string `json:"command_topic"`
	StateTopic        string `json:"state_topic"`
	PayloadOn         string `json:"payload_on"`
	PayloadOff        string `json:"payload_off"`
	PayloadPress      string `json:"payload_press"`
	UnitOfMeasurement string `json:"unit_of_measurement"`
	Device            struct {
		Name         string   `json:"name"`
		Identifiers  []string `json:"identifiers"`
		Manufacturer string   `json:"manufacturer"`
		Model        string   `json:"model"`
	} `json:"device"`
}

// Registry manages the in-memory pool of active devices
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

// HandleDiscoveryMessage processes incoming HA discovery configs
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

	var ha HADiscoveryPayload
	if err := json.Unmarshal(payload, &ha); err != nil {
		return
	}

	id := ha.UniqueID
	if id == "" {
		id = objectID
	}

	name := ha.Name
	if name == "" {
		if ha.Device.Name != "" {
			name = ha.Device.Name + " " + objectID
		} else {
			name = objectID
		}
	}

	payloadOn := ha.PayloadOn
	if payloadOn == "" {
		payloadOn = "ON"
	}
	payloadOff := ha.PayloadOff
	if payloadOff == "" {
		payloadOff = "OFF"
	}
	payloadPress := ha.PayloadPress
	if payloadPress == "" {
		payloadPress = "PRESS"
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
	entity.CommandTopic = ha.CommandTopic
	entity.StateTopic = ha.StateTopic
	entity.PayloadOn = payloadOn
	entity.PayloadOff = payloadOff
	entity.PayloadPress = payloadPress
	entity.UnitOfMeasurement = ha.UnitOfMeasurement

	if ha.StateTopic != "" {
		r.topicToID[ha.StateTopic] = id
		if r.onNewTopic != nil {
			r.onNewTopic(ha.StateTopic)
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

	entity.State = strings.TrimSpace(payload)
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
