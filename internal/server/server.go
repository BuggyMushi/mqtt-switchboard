package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/your-repo/mqtt-switchboard/internal/auth"
	"github.com/your-repo/mqtt-switchboard/internal/config"
	"github.com/your-repo/mqtt-switchboard/internal/discovery"
	"github.com/your-repo/mqtt-switchboard/internal/mqtt"
	"github.com/your-repo/mqtt-switchboard/internal/syslog"
)

const sessionCookieName = "switchboard_session"

// Server encapsulates HTTP service, auth, and state synchronization
type Server struct {
	cfg       *config.Config
	authMgr   *auth.Manager
	registry  *discovery.Registry
	mqttCli   *mqtt.Client
	sseHub    *SSEHub
	logger    syslog.Logger
	webFS     fs.FS
	startTime time.Time
	httpSrv   *http.Server
}

// NewServer initializes HTTP server with embedded web FS and dependencies
func NewServer(
	cfg *config.Config,
	authMgr *auth.Manager,
	reg *discovery.Registry,
	mqttCli *mqtt.Client,
	sseHub *SSEHub,
	log syslog.Logger,
	webFS fs.FS,
) *Server {
	s := &Server{
		cfg:       cfg,
		authMgr:   authMgr,
		registry:  reg,
		mqttCli:   mqttCli,
		sseHub:    sseHub,
		logger:    log,
		webFS:     webFS,
		startTime: time.Now(),
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	s.httpSrv = &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second, // Long timeout for SSE support
		IdleTimeout:  120 * time.Second,
	}

	return s
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Public Endpoints
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	// Authenticated API Endpoints
	mux.HandleFunc("GET /api/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("GET /api/devices", s.requireAuth(s.handleGetDevices))
	mux.HandleFunc("POST /api/devices/{id}/action", s.requireAuth(s.handleDeviceAction))
	mux.HandleFunc("GET /api/layout", s.requireAuth(s.handleGetLayout))
	mux.HandleFunc("POST /api/layout", s.requireAuth(s.handleSaveLayout))
	mux.HandleFunc("GET /api/events", s.requireAuth(s.handleSSE))

	// Embedded Web UI
	mux.HandleFunc("GET /", s.handleWeb)
}

// Start runs HTTP listener
func (s *Server) Start() error {
	s.logger.Info("HTTP Server listening on http://%s", s.cfg.Server.Listen)
	return s.httpSrv.ListenAndServe()
}

// Stop terminates HTTP server cleanly
func (s *Server) Stop() error {
	return s.httpSrv.Close()
}

// Middleware: Require valid session cookie
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		sess, ok := s.authMgr.ValidateSession(cookie.Value)
		if !ok {
			http.Error(w, `{"error":"session expired or invalid"}`, http.StatusUnauthorized)
			return
		}

		// Inject username into request header for handler consumption
		r.Header.Set("X-Authenticated-User", sess.Username)
		next(w, r)
	}
}

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	data, err := fs.ReadFile(s.webFS, "index.html")
	if err != nil {
		http.Error(w, "Embedded UI file not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	resp := map[string]interface{}{
		"status":                   "online",
		"version":                  "1.0.0",
		"uptime_seconds":           int(time.Since(s.startTime).Seconds()),
		"mqtt_connected":           s.mqttCli.IsConnected(),
		"discovered_devices_count": s.registry.Count(),
		"active_sessions":          s.authMgr.ActiveSessionsCount(),
	}
	writeJSON(w, http.StatusOK, resp)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := auth.GetRealIP(r)

	// Check if IP is currently locked out
	if locked, _ := s.authMgr.IsIPLocked(clientIP); locked {
		s.logger.AuthFail("unknown", clientIP, "IP_LOCKED_15M")
		http.Error(w, `{"error":"Too many failed attempts. Try again later."}`, http.StatusTooManyRequests)
		return
	}

	var req loginRequest
	r.Body = http.MaxBytesReader(w, r.Body, 2048) // Limit to 2KB
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request format"}`, http.StatusBadRequest)
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	if !s.cfg.Authenticate(req.Username, req.Password) {
		isLocked := s.authMgr.RecordLoginFailure(clientIP)
		if isLocked {
			s.logger.AuthFail(req.Username, clientIP, "EXCEEDED_MAX_ATTEMPTS_LOCKED")
		} else {
			s.logger.AuthFail(req.Username, clientIP, "INVALID_CREDENTIALS")
		}
		http.Error(w, `{"error":"invalid username or password"}`, http.StatusUnauthorized)
		return
	}

	// Login Success
	s.authMgr.RecordLoginSuccess(clientIP)
	token, err := s.authMgr.CreateSession(req.Username)
	if err != nil {
		http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
		return
	}

	// Set Session Cookie
	maxAge := s.cfg.Server.SessionTimeoutDays * 86400
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.CookieSecure,
	}
	http.SetCookie(w, cookie)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"username": req.Username,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.authMgr.RevokeSession(cookie.Value)
	}

	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.Server.CookieSecure,
	}
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	username := r.Header.Get("X-Authenticated-User")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"username":      username,
	})
}

func (s *Server) handleGetDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.registry.GetAll()
	writeJSON(w, http.StatusOK, devices)
}

type actionRequest struct {
	Action string `json:"action"` // Optional explicit payload e.g. "ON", "OFF", "PRESS"
}

func (s *Server) handleDeviceAction(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	if deviceID == "" {
		http.Error(w, `{"error":"missing device id"}`, http.StatusBadRequest)
		return
	}

	user := r.Header.Get("X-Authenticated-User")
	clientIP := auth.GetRealIP(r)

	var req actionRequest
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	err := s.mqttCli.TriggerDevice(deviceID, req.Action, user, clientIP)
	if err != nil {
		if errors.Is(err, mqtt.ErrDebounced) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"Rate limited. Debounce lock is active."}`, http.StatusTooManyRequests)
			return
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"device_id": deviceID,
	})
}

func getLayoutFilePath() string {
	if _, err := os.Stat("/etc/mqtt-switchboard"); err == nil {
		return "/etc/mqtt-switchboard/layout.json"
	}
	return "./layout.json"
}

type layoutPayload struct {
	Order []string `json:"order"`
}

func (s *Server) handleGetLayout(w http.ResponseWriter, r *http.Request) {
	path := getLayoutFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		writeJSON(w, http.StatusOK, layoutPayload{Order: []string{}})
		return
	}
	var l layoutPayload
	if err := json.Unmarshal(data, &l); err != nil {
		writeJSON(w, http.StatusOK, layoutPayload{Order: []string{}})
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleSaveLayout(w http.ResponseWriter, r *http.Request) {
	user := r.Header.Get("X-Authenticated-User")
	clientIP := auth.GetRealIP(r)

	var req layoutPayload
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid format"}`, http.StatusBadRequest)
		return
	}

	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		http.Error(w, `{"error":"serialize failed"}`, http.StatusInternalServerError)
		return
	}

	path := getLayoutFilePath()
	if err := os.WriteFile(path, data, 0644); err != nil {
		http.Error(w, `{"error":"write failed"}`, http.StatusInternalServerError)
		return
	}

	s.logger.Action(user, "layout", "SAVE_DEFAULT_ORDER", clientIP)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx proxy buffering

	clientChan := make(chan []byte, 16)
	s.sseHub.register <- clientChan
	defer func() {
		s.sseHub.unregister <- clientChan
	}()

	// Send initial greeting / sync ping
	_, _ = w.Write([]byte("event: ready\ndata: connected\n\n"))
	flusher.Flush()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			_, err := w.Write(msg)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}
