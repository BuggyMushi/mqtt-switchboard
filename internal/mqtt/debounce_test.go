package mqtt

import (
	"testing"
	"time"
)

func TestDebouncer_RateLimit(t *testing.T) {
	d := NewDebouncer(200) // 200ms interval for fast unit testing

	deviceID := "relay_1"

	// 1st action: permitted
	if !d.CheckAndLock(deviceID) {
		t.Fatalf("first action must be permitted")
	}

	// 2nd action immediately: rejected
	if d.CheckAndLock(deviceID) {
		t.Fatalf("second action within debounce interval must be rejected")
	}

	// Different device should still be permitted
	if !d.CheckAndLock("relay_2") {
		t.Fatalf("independent device must be permitted")
	}

	// Wait for debounce window to pass
	time.Sleep(250 * time.Millisecond)

	// Now relay_1 should be permitted again
	if !d.CheckAndLock(deviceID) {
		t.Fatalf("action after debounce interval must be permitted")
	}
}
