package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Session represents an authenticated user session
type Session struct {
	Token     string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// IPTracker records login failures for brute-force prevention
type IPTracker struct {
	FailCount  int
	LastFailAt time.Time
	LockedUntil time.Time
}

// Manager orchestrates session storage and rate limiting in memory
type Manager struct {
	mu           sync.RWMutex
	sessions     map[string]Session
	ipTrackers   map[string]*IPTracker
	sessionTTL   time.Duration
	maxAttempts  int
	lockDuration time.Duration
	stopCh       chan struct{}
}

// NewManager initializes session and anti-bruteforce manager
func NewManager(sessionDays, maxAttempts, lockoutMinutes int) *Manager {
	if sessionDays <= 0 {
		sessionDays = 7
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if lockoutMinutes <= 0 {
		lockoutMinutes = 15
	}

	m := &Manager{
		sessions:     make(map[string]Session),
		ipTrackers:   make(map[string]*IPTracker),
		sessionTTL:   time.Duration(sessionDays) * 24 * time.Hour,
		maxAttempts:  maxAttempts,
		lockDuration: time.Duration(lockoutMinutes) * time.Minute,
		stopCh:       make(chan struct{}),
	}

	// Background sweeper to prevent memory leakage
	go m.cleanupWorker()

	return m
}

// Stop cleanly terminates background worker
func (m *Manager) Stop() {
	close(m.stopCh)
}

func (m *Manager) cleanupWorker() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.cleanup()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	// Purge expired sessions
	for token, sess := range m.sessions {
		if now.After(sess.ExpiresAt) {
			delete(m.sessions, token)
		}
	}

	// Purge expired IP trackers
	for ip, tracker := range m.ipTrackers {
		if now.After(tracker.LockedUntil) && now.Sub(tracker.LastFailAt) > (m.lockDuration*2) {
			delete(m.ipTrackers, ip)
		}
	}
}

// CreateSession generates a cryptographic random token and records the session
func (m *Manager) CreateSession(username string) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[token] = Session{
		Token:     token,
		Username:  username,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(m.sessionTTL),
	}

	return token, nil
}

// ValidateSession checks if token is valid and not expired
func (m *Manager) ValidateSession(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}

	m.mu.RLock()
	sess, exists := m.sessions[token]
	m.mu.RUnlock()

	if !exists {
		return Session{}, false
	}

	if time.Now().After(sess.ExpiresAt) {
		m.mu.Lock()
		delete(m.sessions, token)
		m.mu.Unlock()
		return Session{}, false
	}

	return sess, true
}

// RevokeSession removes the session from memory
func (m *Manager) RevokeSession(token string) {
	if token == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

// ActiveSessionsCount returns current valid session count
func (m *Manager) ActiveSessionsCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// IsIPLocked checks whether the IP is currently locked out
func (m *Manager) IsIPLocked(ip string) (bool, time.Duration) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tracker, exists := m.ipTrackers[ip]
	if !exists {
		return false, 0
	}

	if time.Now().Before(tracker.LockedUntil) {
		return true, time.Until(tracker.LockedUntil)
	}

	return false, 0
}

// RecordLoginFailure tracks a failed attempt, locking the IP if threshold exceeded
func (m *Manager) RecordLoginFailure(ip string) (isLockedNow bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	tracker, exists := m.ipTrackers[ip]
	if !exists {
		tracker = &IPTracker{}
		m.ipTrackers[ip] = tracker
	}

	// Reset if previous failures are older than lock window
	if now.Sub(tracker.LastFailAt) > m.lockDuration && now.After(tracker.LockedUntil) {
		tracker.FailCount = 0
	}

	tracker.FailCount++
	tracker.LastFailAt = now

	if tracker.FailCount >= m.maxAttempts {
		tracker.LockedUntil = now.Add(m.lockDuration)
		return true
	}

	return false
}

// RecordLoginSuccess clears failure tracking for the IP
func (m *Manager) RecordLoginSuccess(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.ipTrackers, ip)
}

// GetRealIP extracts client IP by checking reverse proxy headers first
func GetRealIP(r *http.Request) string {
	// 1. Check X-Forwarded-For (can be comma-separated: client, proxy1, proxy2)
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		clientIP := strings.TrimSpace(parts[0])
		if clientIP != "" {
			return clientIP
		}
	}

	// 2. Check X-Real-IP
	xri := r.Header.Get("X-Real-IP")
	if xri != "" {
		return strings.TrimSpace(xri)
	}

	// 3. Fall back to r.RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
