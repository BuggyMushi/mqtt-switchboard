package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthManager_CreateAndValidateSession(t *testing.T) {
	mgr := NewManager(1, 3, 5)
	defer mgr.Stop()

	token, err := mgr.CreateSession("testuser")
	if err != nil {
		t.Fatalf("unexpected error creating session: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("expected 64 char hex token, got %d", len(token))
	}

	sess, ok := mgr.ValidateSession(token)
	if !ok || sess.Username != "testuser" {
		t.Fatalf("failed to validate active session")
	}

	mgr.RevokeSession(token)
	_, ok = mgr.ValidateSession(token)
	if ok {
		t.Fatalf("session should be revoked")
	}
}

func TestAuthManager_BruteForceLockout(t *testing.T) {
	mgr := NewManager(1, 3, 5) // 3 attempts max, 5 min lockout
	defer mgr.Stop()

	ip := "192.168.1.100"

	// 1st & 2nd fail -> not locked
	if mgr.RecordLoginFailure(ip) {
		t.Fatalf("should not lock on 1st fail")
	}
	if mgr.RecordLoginFailure(ip) {
		t.Fatalf("should not lock on 2nd fail")
	}

	// 3rd fail -> locked
	if !mgr.RecordLoginFailure(ip) {
		t.Fatalf("expected lock on 3rd failure")
	}

	locked, duration := mgr.IsIPLocked(ip)
	if !locked || duration <= 0 {
		t.Fatalf("expected IP to be locked with positive duration")
	}

	// Success clears failure tracker
	mgr.RecordLoginSuccess(ip)
	locked, _ = mgr.IsIPLocked(ip)
	if locked {
		t.Fatalf("expected IP to be unlocked after RecordLoginSuccess")
	}
}

func TestGetRealIP(t *testing.T) {
	// Case 1: X-Forwarded-For
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req1.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	req1.RemoteAddr = "127.0.0.1:12345"
	if ip := GetRealIP(req1); ip != "203.0.113.195" {
		t.Fatalf("expected 203.0.113.195, got %s", ip)
	}

	// Case 2: X-Real-IP
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("X-Real-IP", "198.51.100.1")
	req2.RemoteAddr = "127.0.0.1:12345"
	if ip := GetRealIP(req2); ip != "198.51.100.1" {
		t.Fatalf("expected 198.51.100.1, got %s", ip)
	}

	// Case 3: RemoteAddr fallback
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.RemoteAddr = "192.168.1.50:54321"
	if ip := GetRealIP(req3); ip != "192.168.1.50" {
		t.Fatalf("expected 192.168.1.50, got %s", ip)
	}
}

func TestIsPrivateOrLocalIP(t *testing.T) {
	cases := []struct {
		ip       string
		expected bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.1.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"169.254.1.1", true},
		{"fe80::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"203.0.113.195", false},
		{"invalid-ip", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := IsPrivateOrLocalIP(tc.ip); got != tc.expected {
			t.Errorf("IsPrivateOrLocalIP(%q) = %v; expected %v", tc.ip, got, tc.expected)
		}
	}
}

