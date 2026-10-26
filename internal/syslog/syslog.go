package syslog

import (
	"fmt"
)

// Logger defines structured audit logging methods for mqtt-switchboard
type Logger interface {
	Action(user, device, cmd, ip string)
	AuthFail(user, ip, reason string)
	DeviceError(device, reason string)
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
	Close() error
}

func formatAction(user, device, cmd, ip string) string {
	return fmt.Sprintf("ACTION user=%s device=%s cmd=%s ip=%s", user, device, cmd, ip)
}

func formatAuthFail(user, ip, reason string) string {
	return fmt.Sprintf("AUTH_FAIL user=%s ip=%s reason=%s", user, ip, reason)
}

func formatDeviceError(device, reason string) string {
	return fmt.Sprintf("DEVICE_ERROR device=%s reason=%s", device, reason)
}
