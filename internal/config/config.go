package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ServerConfig holds HTTP server options
type ServerConfig struct {
	Listen             string `json:"listen"`
	SessionTimeoutDays int    `json:"session_timeout_days"`
	DebounceMs         int    `json:"debounce_ms"`
	CookieSecure       bool   `json:"cookie_secure"`
}

// MQTTConfig holds MQTT broker and discovery options
type MQTTConfig struct {
	Broker          string `json:"broker"`
	ClientID        string `json:"client_id"`
	Username        string `json:"username"`
	Password        string `json:"password"`
	KeepAliveSec    int    `json:"keep_alive_sec"`
	DiscoveryPrefix string `json:"discovery_prefix"`
}

// SecurityConfig holds anti-bruteforce settings
type SecurityConfig struct {
	MaxLoginAttempts int `json:"max_login_attempts"`
	LockoutMinutes   int `json:"lockout_minutes"`
}

// UserConfig holds login credentials
type UserConfig struct {
	Username     string `json:"username"`
	Password     string `json:"password,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
}

// StaticDevice defines static device fallback
type StaticDevice struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Component    string `json:"component"` // switch, button, sensor
	CmdTopic     string `json:"cmd_topic,omitempty"`
	StateTopic   string `json:"state_topic,omitempty"`
	PayloadOn    string `json:"payload_on,omitempty"`
	PayloadOff   string `json:"payload_off,omitempty"`
	PayloadPress string `json:"payload_press,omitempty"`
}

// Config is the top-level configuration structure
type Config struct {
	Server   ServerConfig   `json:"server"`
	MQTT     MQTTConfig     `json:"mqtt"`
	Security SecurityConfig `json:"security"`
	Users    []UserConfig   `json:"users"`
	Devices  []StaticDevice `json:"devices"`
}

// LoadConfig reads config from JSON file and applies defaults
func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{
			Listen:             "0.0.0.0:8080",
			SessionTimeoutDays: 7,
			DebounceMs:         1000,
			CookieSecure:       false,
		},
		MQTT: MQTTConfig{
			Broker:          "tcp://127.0.0.1:1883",
			ClientID:        "mqtt_switchboard_daemon",
			KeepAliveSec:    30,
			DiscoveryPrefix: "homeassistant",
		},
		Security: SecurityConfig{
			MaxLoginAttempts: 5,
			LockoutMinutes:   15,
		},
	}

	// 1. Auto-discover config file if not explicitly passed
	targetPath := path
	if targetPath == "" {
		candidates := []string{
			"/etc/mqtt-switchboard/config.json",
			"./config.json",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				targetPath = c
				break
			}
		}
	}

	// 2. Load JSON if candidate or path found
	if targetPath != "" {
		data, err := os.ReadFile(targetPath)
		if err == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}

	// 3. Ensure fallback default user if no users defined
	if len(cfg.Users) == 0 {
		cfg.Users = []UserConfig{
			{
				Username: "admin",
				Password: "admin",
			},
		}
	}

	// Validate & ensure fallbacks
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "0.0.0.0:8080"
	}
	if cfg.Server.DebounceMs <= 0 {
		cfg.Server.DebounceMs = 1000
	}
	if cfg.Server.SessionTimeoutDays <= 0 {
		cfg.Server.SessionTimeoutDays = 7
	}
	if cfg.MQTT.Broker == "" {
		cfg.MQTT.Broker = "tcp://127.0.0.1:1883"
	}
	if cfg.MQTT.DiscoveryPrefix == "" {
		cfg.MQTT.DiscoveryPrefix = "homeassistant"
	}
	if cfg.Security.MaxLoginAttempts <= 0 {
		cfg.Security.MaxLoginAttempts = 5
	}
	if cfg.Security.LockoutMinutes <= 0 {
		cfg.Security.LockoutMinutes = 15
	}

	return cfg, nil
}

// Authenticate verifies username and password against configured users
func (c *Config) Authenticate(username, password string) bool {
	for _, u := range c.Users {
		if u.Username != username {
			continue
		}
		// If bcrypt hash provided
		if strings.HasPrefix(u.PasswordHash, "$2a$") || strings.HasPrefix(u.PasswordHash, "$2b$") {
			if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err == nil {
				return true
			}
		}
		// Plaintext fallback / match
		if u.Password != "" && u.Password == password {
			return true
		}
	}
	return false
}
