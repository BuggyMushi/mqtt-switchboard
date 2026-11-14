package server

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/your-repo/mqtt-switchboard/internal/auth"
)

// EinkGlobalSettings holds global display, refresh, and staleness behaviors for /lite
type EinkGlobalSettings struct {
	PureNoCSS            bool `json:"pure_no_css"`             // Ultra-legacy mode without <style> block
	DefaultRefreshSec    int  `json:"default_refresh_sec"`     // 0 = disabled, >0 = meta refresh seconds
	NightRestEnabled     bool `json:"night_rest_enabled"`      // Enable Night Rest mode (18:00 - 08:00)
	NightRestStartHour   int  `json:"night_rest_start_hour"`   // Default 18
	NightRestEndHour     int  `json:"night_rest_end_hour"`     // Default 8
	StaleTimeoutMinutes  int  `json:"stale_timeout_minutes"`   // Mark entity/topic with strikethrough + diagonal slash after N mins (0=off, default 15)
	MaxUnassignedItems   int  `json:"max_unassigned_items"`    // Cap unassigned items on single page (e.g. 30 or 50 for 10"~20" screens)
	ShowUnassigned       bool `json:"show_unassigned"`         // Whether to render newly discovered unassigned devices below custom boxes
	AllowAllLAN          bool `json:"allow_all_lan"`           // Allow any LAN device without login if MAC whitelist is empty
}

// AuthorizedEinkDevice binds a physical E-Ink MAC (from /proc/net/arp) or token to a friendly name & profile
type AuthorizedEinkDevice struct {
	MAC     string `json:"mac"`               // Format: AA:BB:CC:DD:EE:FF
	Name    string `json:"name"`              // Friendly name logged in Syslog when buttons are pressed, e.g. "雞舍大門_Kindle"
	Token   string `json:"token,omitempty"`   // Optional URL token fallback (?dev=...) if across routed subnet
	Profile string `json:"profile,omitempty"` // Reserved for profile selection (default: "default")
}

// EinkBannerConfig defines a top broadcast / plain-text announcement slot (stores up to 3 recent messages)
type EinkBannerConfig struct {
	ID          string `json:"id"`
	Label       string `json:"label"`                  // e.g. "📢 廣播通知" or "🐣 翻蛋狀態"
	Topic       string `json:"topic"`                  // Arbitrary MQTT topic e.g. "Farm/Broadcast" or "Farm/Eggroller/RptMess"
	JSONKey     string `json:"json_key,omitempty"`     // Optional JSON key extraction; empty = raw plain text
	LatestSize  string `json:"latest_size,omitempty"`  // Font size for the latest (#1) message, e.g. "28px", "36px", "44px"
	HistorySize string `json:"history_size,omitempty"` // Font size for #2 and #3 historical messages, e.g. "14px", "16px"
	MaxHistory  int    `json:"max_history,omitempty"`  // 1 to 3 (default 3)
}

// EinkDataSlot represents one data source (either a discovered Entity or an arbitrary MQTT Topic)
type EinkDataSlot struct {
	SourceType string `json:"source_type"`        // "entity" or "topic"
	EntityID   string `json:"entity_id,omitempty"`// Used when source_type == "entity"
	Topic      string `json:"topic,omitempty"`    // Used when source_type == "topic"
	JSONKey    string `json:"json_key,omitempty"` // Optional JSON field when source_type == "topic"
	Prefix     string `json:"prefix,omitempty"`   // e.g. "目標 " or "D"
	Unit       string `json:"unit,omitempty"`     // e.g. "°C" or "%" (overrides entity default if set)
}

// EinkMetricCell represents a single dense cell in a Box (can combine Primary + Secondary slots, e.g. [育雛室: 32.5°C / 目標 33.0°C])
type EinkMetricCell struct {
	ID        string        `json:"id"`
	Label     string        `json:"label"`               // Custom cell header e.g. "育雛室"
	Primary   EinkDataSlot  `json:"primary"`             // Main value slot
	Secondary *EinkDataSlot `json:"secondary,omitempty"` // Optional second value slot combined with " / "
}

// EinkControlItem represents a selected control button/switch picked from any device (e.g. B4, B5, C6, C7)
type EinkControlItem struct {
	EntityID string `json:"entity_id"`
	Alias    string `json:"alias,omitempty"`  // Custom short label; if empty, auto-cleaned entity name is used
	Action   string `json:"action,omitempty"` // Optional explicit payload override; default empty = standard toggle/press
}

// EinkCustomBox represents a Composite Container (e.g. Box A packing selected items from Device B & Device C + custom topics)
type EinkCustomBox struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`        // e.g. "A：孵蛋與育雛重點監控"
	Subtitle    string            `json:"subtitle,omitempty"`
	MetricCols  int               `json:"metric_cols"`  // Number of columns per row for dense metrics (default 3, supports 1~6)
	ControlCols int               `json:"control_cols"` // Number of columns per row for controls (default 2, supports 1~4)
	ValueSize   string            `json:"value_size,omitempty"` // Font size for metric values, e.g. "14px", "18px"
	Metrics     []EinkMetricCell  `json:"metrics"`
	Controls    []EinkControlItem `json:"controls"`
}

// EinkLayoutConfig is the unified configuration persisted in layout.json
type EinkLayoutConfig struct {
	Order             []string               `json:"order"`              // Modern UI card order (backward compatible)
	Global            EinkGlobalSettings     `json:"global"`             // E-Ink global settings
	AuthorizedDevices []AuthorizedEinkDevice `json:"authorized_devices"` // MAC / Token whitelist & device naming
	TopBanners        []EinkBannerConfig     `json:"top_banners"`        // Top broadcast / announcement blocks (up to 3 history items)
	CustomBoxes       []EinkCustomBox        `json:"custom_boxes"`       // Custom dense composite boxes (A packs B4,B5 + C1,C6,C7)
	HiddenUnassigned  []string               `json:"hidden_unassigned"`  // Device IDs or Entity IDs excluded from unassigned auto-list
}

var (
	einkConfigMu sync.RWMutex
	cachedLayout *EinkLayoutConfig
)

func defaultEinkLayoutConfig() *EinkLayoutConfig {
	return &EinkLayoutConfig{
		Order: []string{},
		Global: EinkGlobalSettings{
			PureNoCSS:           false,
			DefaultRefreshSec:   60,
			NightRestEnabled:    false,
			NightRestStartHour:  18,
			NightRestEndHour:    8,
			StaleTimeoutMinutes: 15,
			MaxUnassignedItems:  30,
			ShowUnassigned:      true,
			AllowAllLAN:         false,
		},
		AuthorizedDevices: []AuthorizedEinkDevice{},
		TopBanners:        []EinkBannerConfig{},
		CustomBoxes:       []EinkCustomBox{},
		HiddenUnassigned:  []string{},
	}
}

func normalizeEinkLayout(cfg *EinkLayoutConfig) {
	if cfg.Order == nil {
		cfg.Order = []string{}
	}
	if cfg.Global.NightRestStartHour == 0 && cfg.Global.NightRestEndHour == 0 {
		cfg.Global.NightRestStartHour = 18
		cfg.Global.NightRestEndHour = 8
	}
	if cfg.Global.MaxUnassignedItems <= 0 {
		cfg.Global.MaxUnassignedItems = 30
	}
	if cfg.Global.StaleTimeoutMinutes < 0 {
		cfg.Global.StaleTimeoutMinutes = 0
	}
	if cfg.AuthorizedDevices == nil {
		cfg.AuthorizedDevices = []AuthorizedEinkDevice{}
	}
	for i := range cfg.AuthorizedDevices {
		cfg.AuthorizedDevices[i].MAC = auth.NormalizeMAC(cfg.AuthorizedDevices[i].MAC)
	}
	if cfg.TopBanners == nil {
		cfg.TopBanners = []EinkBannerConfig{}
	}
	for i := range cfg.TopBanners {
		if cfg.TopBanners[i].LatestSize == "" {
			cfg.TopBanners[i].LatestSize = "28px"
		} else if _, err := strconv.Atoi(cfg.TopBanners[i].LatestSize); err == nil {
			cfg.TopBanners[i].LatestSize += "px"
		}
		if cfg.TopBanners[i].HistorySize == "" {
			cfg.TopBanners[i].HistorySize = "14px"
		} else if _, err := strconv.Atoi(cfg.TopBanners[i].HistorySize); err == nil {
			cfg.TopBanners[i].HistorySize += "px"
		}
		if cfg.TopBanners[i].MaxHistory <= 0 || cfg.TopBanners[i].MaxHistory > 3 {
			cfg.TopBanners[i].MaxHistory = 3
		}
	}
	if cfg.CustomBoxes == nil {
		cfg.CustomBoxes = []EinkCustomBox{}
	}
	for i := range cfg.CustomBoxes {
		if cfg.CustomBoxes[i].MetricCols <= 0 {
			cfg.CustomBoxes[i].MetricCols = 3
		}
		if cfg.CustomBoxes[i].ControlCols <= 0 {
			cfg.CustomBoxes[i].ControlCols = 2
		}
		if cfg.CustomBoxes[i].ValueSize != "" {
			if _, err := strconv.Atoi(cfg.CustomBoxes[i].ValueSize); err == nil {
				cfg.CustomBoxes[i].ValueSize += "px"
			}
		}
		if cfg.CustomBoxes[i].Metrics == nil {
			cfg.CustomBoxes[i].Metrics = []EinkMetricCell{}
		}
		if cfg.CustomBoxes[i].Controls == nil {
			cfg.CustomBoxes[i].Controls = []EinkControlItem{}
		}
	}
	if cfg.HiddenUnassigned == nil {
		cfg.HiddenUnassigned = []string{}
	}
}

func cloneEinkLayoutConfig(src *EinkLayoutConfig) *EinkLayoutConfig {
	if src == nil {
		return defaultEinkLayoutConfig()
	}
	b, err := json.Marshal(src)
	if err != nil {
		return defaultEinkLayoutConfig()
	}
	var dst EinkLayoutConfig
	if err := json.Unmarshal(b, &dst); err != nil {
		return defaultEinkLayoutConfig()
	}
	return &dst
}

// LoadOrGetEinkLayout loads layout.json from disk once or returns a deep copy of the in-memory config, and subscribes custom MQTT topics
func (s *Server) LoadOrGetEinkLayout() *EinkLayoutConfig {
	einkConfigMu.RLock()
	if cachedLayout != nil {
		cpy := cloneEinkLayoutConfig(cachedLayout)
		einkConfigMu.RUnlock()
		return cpy
	}
	einkConfigMu.RUnlock()

	einkConfigMu.Lock()
	defer einkConfigMu.Unlock()

	cfg := defaultEinkLayoutConfig()
	path := getLayoutFilePath()
	if data, err := os.ReadFile(path); err == nil {
		// Check if it was legacy format without "global" field
		var raw map[string]interface{}
		if json.Unmarshal(data, &raw) == nil {
			_, hasGlobal := raw["global"]
			_ = json.Unmarshal(data, cfg)
			if !hasGlobal {
				cfg.Global.ShowUnassigned = true
				cfg.Global.DefaultRefreshSec = 60
				cfg.Global.StaleTimeoutMinutes = 15
				cfg.Global.MaxUnassignedItems = 30
			}
		}
	}
	normalizeEinkLayout(cfg)
	cachedLayout = cfg

	// Subscribe to any custom MQTT topics referenced in banners or metric slots
	s.syncCustomTopicSubscriptions(cfg)

	return cloneEinkLayoutConfig(cachedLayout)
}

// SaveEinkLayout persists the updated configuration to disk (only when admin saves in Studio) and updates subscriptions
func (s *Server) SaveEinkLayout(cfg *EinkLayoutConfig) error {
	normalizeEinkLayout(cfg)

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	path := getLayoutFilePath()
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	einkConfigMu.Lock()
	cachedLayout = cfg
	einkConfigMu.Unlock()

	s.syncCustomTopicSubscriptions(cfg)
	return nil
}

func (s *Server) syncCustomTopicSubscriptions(cfg *EinkLayoutConfig) {
	if s.registry == nil {
		return
	}
	for _, b := range cfg.TopBanners {
		if t := strings.TrimSpace(b.Topic); t != "" {
			s.registry.EnsureCustomTopic(t)
		}
	}
	for _, box := range cfg.CustomBoxes {
		for _, m := range box.Metrics {
			if m.Primary.SourceType == "topic" && strings.TrimSpace(m.Primary.Topic) != "" {
				s.registry.EnsureCustomTopic(strings.TrimSpace(m.Primary.Topic))
			}
			if m.Secondary != nil && m.Secondary.SourceType == "topic" && strings.TrimSpace(m.Secondary.Topic) != "" {
				s.registry.EnsureCustomTopic(strings.TrimSpace(m.Secondary.Topic))
			}
		}
	}
}

// ResolveSlotValue evaluates an EinkDataSlot (either entity or custom topic) and checks if it is stale
func (s *Server) ResolveSlotValue(slot EinkDataSlot, staleMinutes int) (valStr string, isStale bool) {
	if slot.SourceType == "topic" {
		topic := strings.TrimSpace(slot.Topic)
		if topic == "" {
			return "--", false
		}
		ct, ok := s.registry.GetCustomTopic(topic)
		if !ok || ct.LastUpdated.IsZero() {
			return "--", true
		}
		rawVal := ct.LastPayload
		if slot.JSONKey != "" && ct.LastJSON != nil {
			if v, exists := ct.LastJSON[slot.JSONKey]; exists && v != nil {
				rawVal = discoveryFormatValue(v)
			} else {
				rawVal = "--"
			}
		}
		if rawVal == "" {
			rawVal = "--"
		}
		out := slot.Prefix + rawVal
		if slot.Unit != "" && rawVal != "--" {
			out += slot.Unit
		}
		stale := isTimestampStale(ct.LastUpdated, staleMinutes)
		return out, stale
	}

	// Default: "entity"
	entID := strings.TrimSpace(slot.EntityID)
	if entID == "" {
		return "--", false
	}
	ent, ok := s.registry.GetByID(entID)
	if !ok {
		return "離線", true
	}
	rawVal := ent.State
	if rawVal == "" || rawVal == "UNKNOWN" {
		rawVal = "--"
	}
	unit := slot.Unit
	if unit == "" {
		unit = ent.UnitOfMeasurement
	}
	out := slot.Prefix + rawVal
	if unit != "" && rawVal != "--" {
		out += " " + unit
	}
	stale := (ent.State == "" || ent.State == "UNKNOWN") || isTimestampStale(ent.LastUpdated, staleMinutes)
	return out, stale
}

func discoveryFormatValue(v interface{}) string {
	return fmt.Sprintf("%v", v)
}

func discoveryUnmarshalJSON(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}


func isTimestampStale(t time.Time, staleMinutes int) bool {
	if staleMinutes <= 0 {
		return false
	}
	if t.IsZero() {
		return true
	}
	return time.Since(t) > time.Duration(staleMinutes)*time.Minute
}

