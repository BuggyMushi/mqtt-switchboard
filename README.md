# mqtt-switchboard

<p align="center">
  <strong>Ultra-lightweight Web-to-MQTT switchboard and IoT gateway service specifically designed for OpenWrt routers.</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/OpenWrt-24.10%20%2F%2025.12-0055aa?style=flat-square" alt="OpenWrt Version">
  <img src="https://img.shields.io/badge/RAM%20Footprint-%3C%2015%20MB-brightgreen?style=flat-square" alt="RAM Usage">
  <img src="https://img.shields.io/badge/Dependencies-Zero-success?style=flat-square" alt="Dependencies">
  <img src="https://img.shields.io/badge/License-MIT-blue?style=flat-square" alt="License">
</p>

---

## 📖 Overview

**`mqtt-switchboard`** is a production-ready, zero-dependency IoT control panel daemon engineered for embedded Linux and OpenWrt routers. It bridges web clients to local MQTT brokers (such as Mosquitto) through a single static binary with embedded assets, robust anti-bruteforce security, hardware debounce protection, and automatic Home Assistant MQTT Discovery.

### ✨ Key Features

- **⚡ Ultra-Low Resource Usage**: Single static binary compiled with `CGO_ENABLED=0`. Constant RSS memory footprint strictly throttled to **8MB ~ 12MB** (guaranteed < 15MB).
- **📦 Zero External Runtime Dependencies**: Embedded modern single-page dashboard (`//go:embed`) with zero external CDN / Google Fonts requirements. Fully operational in air-gapped / offline local networks.
- **📱 Dual-Mode Display (Mobile Dark Glassmorphism + E-Ink Monochrome)**:
  - **Standard Mode**: Sleek dark card grid layout with real-time feedback.
  - **E-Ink Mode (`?eink=1`)**: High-contrast pure black-and-white theme (`#000` / `#FFF`), zero transitions, and no spinning animations to eliminate ghosting and screen flickering on e-paper devices.
- **🔌 Home Assistant MQTT Discovery**: Auto-detects and provisions device cards dynamically by listening to `<discovery_prefix>/+/+/config` topics (`switch`, `light`, `button`, `sensor`, `binary_sensor`).
- **🛡️ Physical Relay Debounce Protection**: 1-second backend rate-limiting per device to prevent relay contact chattering and hardware burnout.
- **🔒 Industrial-Grade Security**:
  - In-memory session store with cryptographically secure tokens.
  - Anti-bruteforce protection: automatically locks client IP for 15 minutes after 5 consecutive failed login attempts.
  - Reverse proxy real-IP resolution (`X-Forwarded-For` and `X-Real-IP`).
- **💾 Zero Flash Disk Wear**: No local SQLite databases or disk log writes. Direct structured POSIX logging to system `/dev/log` (`syslog`).
- **🌐 OpenWrt Ecosystem Compatible**: Built-in `procd` init script with `respawn` daemon monitoring and OpenWrt 24.10 / 25.12 package recipe (`golang-package.mk`).

---

## 🏗️ Architecture

```text
  [ Browser / E-Ink Tablet ]
              │ (HTTPS / HTTP)
              ▼
    [ Reverse Proxy / Nginx ]
              │ (127.0.0.1:8080)
              ▼
   ┌──────────────────────────────────────────────────────────┐
   │                  mqtt-switchboard                        │
   │  - Embedded UI (Dark / E-Ink)                            │
   │  - Real-IP Extraction & Anti-Bruteforce                  │
   │  - Session Manager & SSE Real-time Hub                   │
   │  - Relay Debounce Protection (1s Lock)                   │
   │  - HA MQTT Discovery Parser                              │
   └──────────────────────────┬───────────────────────────────┘
                              │ Pub / Sub (127.0.0.1:1883)
                              ▼
                   [ Local Mosquitto Broker ]
                              │ (MQTT / Wi-Fi)
                              ▼
                [ ESP32 / ESP8266 IoT Nodes ]
```

---

## 🚀 Quick Start

### 1. Download Binary
Download the pre-compiled static binary matching your router architecture from GitHub Releases:
- `x86_64`: `mqtt-switchboard-linux-amd64`
- `aarch64` (ARM64): `mqtt-switchboard-linux-arm64`
- `armv7` (Cortex-A7/A9): `mqtt-switchboard-linux-armv7`
- `mipsle` (MT7621 / MT7628): `mqtt-switchboard-linux-mipsle`

### 2. Install on OpenWrt
```bash
# Upload binary and set executable permission
scp mqtt-switchboard-linux-mipsle root@192.168.1.1:/usr/bin/mqtt-switchboard
ssh root@192.168.1.1 "chmod +x /usr/bin/mqtt-switchboard"

# Upload service init script and UCI config
scp openwrt/files/mqtt-switchboard.init root@192.168.1.1:/etc/init.d/mqtt-switchboard
scp openwrt/files/mqtt-switchboard.config root@192.168.1.1:/etc/config/mqtt-switchboard

# Enable and start daemon
ssh root@192.168.1.1
chmod +x /etc/init.d/mqtt-switchboard
/etc/init.d/mqtt-switchboard enable
/etc/init.d/mqtt-switchboard start
```

### 3. Access Dashboard
- Standard Web UI: `http://<router-ip>:8080/`
- E-Ink Dedicated UI: `http://<router-ip>:8080/?eink=1`
- Default Credentials: `admin` / `admin`

---

## ⚙️ Configuration

Copy `config.example.json` to `/etc/mqtt-switchboard/config.json`:

```json
{
  "server": {
    "listen": "127.0.0.1:8080",
    "session_timeout_days": 7,
    "debounce_ms": 1000,
    "cookie_secure": false
  },
  "mqtt": {
    "broker": "tcp://127.0.0.1:1883",
    "client_id": "mqtt_switchboard_daemon",
    "username": "",
    "password": "",
    "keep_alive_sec": 30,
    "discovery_prefix": "homeassistant"
  },
  "security": {
    "max_login_attempts": 5,
    "lockout_minutes": 15
  },
  "users": [
    {
      "username": "admin",
      "password": "your_secure_password"
    }
  ]
}
```

---

## 📡 REST API Reference

| Endpoint | Method | Auth | Description |
| :--- | :--- | :--- | :--- |
| `/api/health` | `GET` | None | Returns daemon status, MQTT connection, and device counts. |
| `/api/login` | `POST` | None | Authenticates user and issues HTTP session cookie. |
| `/api/logout` | `POST` | Cookie | Terminates active session. |
| `/api/me` | `GET` | Cookie | Returns current authenticated username. |
| `/api/devices` | `GET` | Cookie | Lists all discovered & static devices with states. |
| `/api/devices/{id}/action` | `POST` | Cookie | Dispatches control action to MQTT (with debounce protection). |
| `/api/events` | `GET` | Cookie | Server-Sent Events (SSE) stream for live device state updates. |

---

## 🛠️ Building from Source

### Prerequisites
- Go 1.22+
- Make

```bash
# Build for current host platform
make build

# Cross-compile for all OpenWrt target architectures into dist/
make cross-compile
```

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).
