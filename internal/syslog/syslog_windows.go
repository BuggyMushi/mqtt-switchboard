//go:build windows

package syslog

import (
	"fmt"
	"os"
)

type winLogger struct {
	tag string
}

// NewLogger creates a Windows-compatible console logger simulating syslog
func NewLogger(tag string) Logger {
	return &winLogger{tag: tag}
}

func (l *winLogger) Action(user, device, cmd, ip string) {
	fmt.Fprintf(os.Stderr, "[%s:ACTION] %s\n", l.tag, formatAction(user, device, cmd, ip))
}

func (l *winLogger) AuthFail(user, ip, reason string) {
	fmt.Fprintf(os.Stderr, "[%s:AUTH_FAIL] %s\n", l.tag, formatAuthFail(user, ip, reason))
}

func (l *winLogger) DeviceError(device, reason string) {
	fmt.Fprintf(os.Stderr, "[%s:DEVICE_ERROR] %s\n", l.tag, formatDeviceError(device, reason))
}

func (l *winLogger) Info(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:INFO] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *winLogger) Warn(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:WARN] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *winLogger) Error(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:ERROR] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *winLogger) Close() error {
	return nil
}
