package mqtt

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/your-repo/mqtt-switchboard/internal/config"
	"github.com/your-repo/mqtt-switchboard/internal/discovery"
	"github.com/your-repo/mqtt-switchboard/internal/syslog"
)

var (
	// ErrDebounced returned when action is rejected due to 1-second debounce lock
	ErrDebounced = errors.New("command rejected: debounce rate limit exceeded")
	// ErrNoCommandTopic returned if device has no command topic
	ErrNoCommandTopic = errors.New("device does not support commands")
)

// Debouncer provides 1-second per-device rate limiting to protect physical relays
type Debouncer struct {
	mu       sync.Mutex
	lastSeen map[string]time.Time
	interval time.Duration
}

// NewDebouncer creates debouncer with configured interval
func NewDebouncer(ms int) *Debouncer {
	if ms <= 0 {
		ms = 1000
	}
	return &Debouncer{
		lastSeen: make(map[string]time.Time),
		interval: time.Duration(ms) * time.Millisecond,
	}
}

// CheckAndLock returns true if action is permitted and locks device timestamp
func (d *Debouncer) CheckAndLock(deviceID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	last, exists := d.lastSeen[deviceID]
	if exists && now.Sub(last) < d.interval {
		return false
	}

	d.lastSeen[deviceID] = now
	return true
}

// Client wraps Paho MQTT client with discovery and debounce handling
type Client struct {
	cfg          *config.MQTTConfig
	client       pahomqtt.Client
	registry     *discovery.Registry
	debouncer    *Debouncer
	logger       syslog.Logger
	subscribedMu sync.Mutex
	subscribed   map[string]bool
	isConnected  bool
}

// NewClient initializes MQTT client manager
func NewClient(cfg *config.MQTTConfig, debounceMs int, reg *discovery.Registry, log syslog.Logger) *Client {
	c := &Client{
		cfg:        cfg,
		registry:   reg,
		debouncer:  NewDebouncer(debounceMs),
		logger:     log,
		subscribed: make(map[string]bool),
	}
	return c
}

// Start connects to the broker and establishes discovery subscriptions
func (c *Client) Start() error {
	opts := pahomqtt.NewClientOptions()
	opts.AddBroker(c.cfg.Broker)

	clientID := c.cfg.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("mqtt_switchboard_%d", time.Now().UnixNano()%100000)
	}
	opts.SetClientID(clientID)

	if c.cfg.Username != "" {
		opts.SetUsername(c.cfg.Username)
		opts.SetPassword(c.cfg.Password)
	}
	opts.SetKeepAlive(time.Duration(c.cfg.KeepAliveSec) * time.Second)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetCleanSession(true)

	opts.OnConnect = func(client pahomqtt.Client) {
		c.isConnected = true
		c.logger.Info("[MQTT] Successfully connected to broker %s (client_id: %s)", c.cfg.Broker, clientID)
		c.subscribeDiscovery()
		c.resubscribeAllStates()
	}

	opts.OnConnectionLost = func(client pahomqtt.Client, err error) {
		c.isConnected = false
		c.logger.Warn("[MQTT] Broker connection lost (%s): %v", c.cfg.Broker, err)
	}

	c.logger.Info("[MQTT] Connecting to broker: %s (client_id: %s, user: '%s')", c.cfg.Broker, clientID, c.cfg.Username)
	c.client = pahomqtt.NewClient(opts)
	token := c.client.Connect()
	if token.Wait() && token.Error() != nil {
		c.logger.Error("[MQTT] Initial connection to broker (%s) failed: %v (auto-retry active in background)", c.cfg.Broker, token.Error())
		return token.Error()
	}

	return nil
}

// IsConnected returns current broker connection state
func (c *Client) IsConnected() bool {
	return c.client != nil && c.client.IsConnected()
}

// Stop cleanly disconnects from MQTT broker
func (c *Client) Stop() {
	if c.client != nil && c.client.IsConnected() {
		c.client.Disconnect(250)
	}
}

func (c *Client) subscribeDiscovery() {
	prefix := c.cfg.DiscoveryPrefix
	pattern := prefix + "/#"
	token := c.client.Subscribe(pattern, 0, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		topic := msg.Topic()
		if strings.HasSuffix(topic, "/config") {
			payload := make([]byte, len(msg.Payload()))
			copy(payload, msg.Payload())
			c.logger.Info("[MQTT:Discovery] Received config on %s (bytes: %d)", topic, len(payload))
			go c.registry.HandleDiscoveryMessage(prefix, topic, payload)
		}
	})
	token.Wait()
	c.logger.Info("Subscribed to HA Discovery wildcard: %s", pattern)
}

// SubscribeStateTopic dynamically subscribes to a device state topic in background
func (c *Client) SubscribeStateTopic(topic string) {
	if topic == "" || c.client == nil || !c.client.IsConnected() {
		return
	}

	c.subscribedMu.Lock()
	if c.subscribed[topic] {
		c.subscribedMu.Unlock()
		return
	}
	c.subscribed[topic] = true
	c.subscribedMu.Unlock()

	go func(targetTopic string) {
		c.logger.Info("[MQTT] Subscribing to state topic: %s", targetTopic)
		token := c.client.Subscribe(targetTopic, 0, func(_ pahomqtt.Client, msg pahomqtt.Message) {
			c.logger.Info("[MQTT:State] %s -> %s", msg.Topic(), string(msg.Payload()))
			c.registry.UpdateStateByTopic(msg.Topic(), string(msg.Payload()))
		})
		if token.Wait() && token.Error() != nil {
			c.logger.Warn("[MQTT] Failed to subscribe to state topic %s: %v", targetTopic, token.Error())
			c.subscribedMu.Lock()
			delete(c.subscribed, targetTopic)
			c.subscribedMu.Unlock()
		} else {
			c.logger.Info("[MQTT] Successfully subscribed to state topic: %s", targetTopic)
		}
	}(topic)
}

func (c *Client) resubscribeAllStates() {
	devices := c.registry.GetAll()
	for _, dev := range devices {
		if dev.StateTopic != "" {
			c.SubscribeStateTopic(dev.StateTopic)
		}
	}
}

// TriggerDevice executes a command payload on device with debounce protection
func (c *Client) TriggerDevice(deviceID, requestedAction, user, clientIP string) error {
	entity, exists := c.registry.GetByID(deviceID)
	if !exists {
		return fmt.Errorf("device %s not found", deviceID)
	}

	if entity.CommandTopic == "" {
		return ErrNoCommandTopic
	}

	// 1-second debounce verification
	if !c.debouncer.CheckAndLock(deviceID) {
		c.logger.Warn("DEBOUNCE_REJECT device=%s user=%s ip=%s", deviceID, user, clientIP)
		return ErrDebounced
	}

	var payload string
	switch entity.Component {
	case "button":
		payload = entity.PayloadPress
		if payload == "" {
			payload = "PRESS"
		}
	case "switch", "light":
		if requestedAction != "" {
			payload = requestedAction
		} else {
			// Toggle logic
			if entity.State == entity.PayloadOn {
				payload = entity.PayloadOff
			} else {
				payload = entity.PayloadOn
			}
		}
	default:
		if requestedAction != "" {
			payload = requestedAction
		} else {
			payload = entity.PayloadOn
		}
	}

	token := c.client.Publish(entity.CommandTopic, 0, false, payload)
	if token.Wait() && token.Error() != nil {
		c.logger.DeviceError(deviceID, fmt.Sprintf("MQTT_PUBLISH_FAIL: %v", token.Error()))
		return token.Error()
	}

	c.logger.Action(user, deviceID, payload, clientIP)
	return nil
}
