package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/your-repo/mqtt-switchboard/internal/auth"
	"github.com/your-repo/mqtt-switchboard/internal/config"
	"github.com/your-repo/mqtt-switchboard/internal/discovery"
	"github.com/your-repo/mqtt-switchboard/internal/mqtt"
	"github.com/your-repo/mqtt-switchboard/internal/server"
	"github.com/your-repo/mqtt-switchboard/internal/syslog"
	"github.com/your-repo/mqtt-switchboard/web"
)

var (
	version = "1.0.0"

	flagConfig       = flag.String("config", "", "Path to config.json (optional)")
	flagListen       = flag.String("listen", "", "Override HTTP listen address (e.g. 127.0.0.1:8080)")
	flagBroker       = flag.String("broker", "", "Override MQTT Broker URL (e.g. tcp://127.0.0.1:1883)")
	flagDiscovery    = flag.String("discovery", "", "Override HA Discovery Prefix (e.g. homeassistant)")
	flagDebounceMs   = flag.Int("debounce", 0, "Override Relay Debounce in milliseconds (e.g. 1000)")
	flagCookieSecure = flag.Bool("cookie-secure", false, "Require HTTPS for session cookie")
	flagShowVersion  = flag.Bool("version", false, "Print version and exit")
)

func main() {
	flag.Parse()

	if *flagShowVersion {
		fmt.Printf("mqtt-switchboard version %s\n", version)
		os.Exit(0)
	}

	// Embedded system memory tuning: trigger GC at 50% heap increase to keep RAM < 15MB
	debug.SetGCPercent(50)

	// 1. Initialize Syslog logger (falls back to Stderr if /dev/log unavailable)
	logger := syslog.NewLogger("mqtt-switchboard")
	defer logger.Close()

	logger.Info("Starting mqtt-switchboard version %s", version)

	// 2. Load Configuration
	cfg, err := config.LoadConfig(*flagConfig)
	if err != nil {
		logger.Error("Failed to load config: %v", err)
		os.Exit(1)
	}

	// Apply CLI Flag overrides (allows OpenWrt procd / UCI dynamic options)
	if *flagListen != "" {
		cfg.Server.Listen = *flagListen
	}
	if *flagBroker != "" {
		cfg.MQTT.Broker = *flagBroker
	}
	if *flagDiscovery != "" {
		cfg.MQTT.DiscoveryPrefix = *flagDiscovery
	}
	if *flagDebounceMs > 0 {
		cfg.Server.DebounceMs = *flagDebounceMs
	}
	if *flagCookieSecure {
		cfg.Server.CookieSecure = true
	}

	// 3. Initialize Auth & Session Manager
	authMgr := auth.NewManager(
		cfg.Server.SessionTimeoutDays,
		cfg.Security.MaxLoginAttempts,
		cfg.Security.LockoutMinutes,
	)
	defer authMgr.Stop()

	// 4. Initialize SSE Hub
	sseHub := server.NewSSEHub()
	defer sseHub.Stop()

	// 5. Initialize Device Registry with SSE broadcast callback
	var mqttClient *mqtt.Client
	registry := discovery.NewRegistry(
		func(stateTopic string) {
			if mqttClient != nil {
				mqttClient.SubscribeStateTopic(stateTopic)
			}
		},
		func(entity *discovery.DeviceEntity) {
			sseHub.BroadcastDeviceEvent(entity)
		},
	)

	// Register static devices from config if any
	for _, dev := range cfg.Devices {
		registry.RegisterStatic(
			dev.ID,
			dev.Name,
			dev.Component,
			dev.CmdTopic,
			dev.StateTopic,
			dev.PayloadOn,
			dev.PayloadOff,
			dev.PayloadPress,
		)
	}

	// 6. Initialize and start MQTT Client
	mqttClient = mqtt.NewClient(&cfg.MQTT, cfg.Server.DebounceMs, registry, logger)
	if err := mqttClient.Start(); err != nil {
		logger.Warn("Initial MQTT connection failed (auto-reconnect active): %v", err)
	}
	defer mqttClient.Stop()

	// 7. Start HTTP Server using embedded web assets
	httpServer := server.NewServer(cfg, authMgr, registry, mqttClient, sseHub, logger, web.Assets)
	go func() {
		if err := httpServer.Start(); err != nil {
			logger.Info("HTTP Server stopped: %v", err)
		}
	}()

	// 9. Graceful Shutdown on SIGINT / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("Shutting down mqtt-switchboard...")
	_ = httpServer.Stop()
	logger.Info("mqtt-switchboard stopped cleanly")
}
