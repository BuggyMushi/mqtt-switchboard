//go:build !windows

package syslog

import (
	"fmt"
	"log"
	syslogpkg "log/syslog"
	"os"
)

type unixSyslogger struct {
	writer     *syslogpkg.Writer
	tag        string
	isTerminal bool
}

// NewLogger creates a POSIX /dev/log syslog wrapper with graceful fallback to os.Stderr
func NewLogger(tag string) Logger {
	isTerm := false
	if fi, err := os.Stderr.Stat(); err == nil {
		isTerm = (fi.Mode() & os.ModeCharDevice) != 0
	}

	w, err := syslogpkg.New(syslogpkg.LOG_INFO|syslogpkg.LOG_DAEMON, tag)
	if err != nil {
		log.Printf("[syslog] Warning: unable to connect to /dev/log (%v), falling back to stderr", err)
		return &fallbackLogger{tag: tag}
	}
	return &unixSyslogger{writer: w, tag: tag, isTerminal: isTerm}
}

func (l *unixSyslogger) Action(user, device, cmd, ip string) {
	msg := formatAction(user, device, cmd, ip)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:ACTION] %s\n", l.tag, msg)
	}
	_ = l.writer.Info(msg)
}

func (l *unixSyslogger) AuthFail(user, ip, reason string) {
	msg := formatAuthFail(user, ip, reason)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:AUTH_FAIL] %s\n", l.tag, msg)
	}
	_ = l.writer.Warning(msg)
}

func (l *unixSyslogger) DeviceError(device, reason string) {
	msg := formatDeviceError(device, reason)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:DEVICE_ERROR] %s\n", l.tag, msg)
	}
	_ = l.writer.Err(msg)
}

func (l *unixSyslogger) Debug(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:DEBUG] %s\n", l.tag, msg)
	}
	_ = l.writer.Debug(msg)
}

func (l *unixSyslogger) Info(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:INFO] %s\n", l.tag, msg)
	}
	_ = l.writer.Info(msg)
}

func (l *unixSyslogger) Warn(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:WARN] %s\n", l.tag, msg)
	}
	_ = l.writer.Warning(msg)
}

func (l *unixSyslogger) Error(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if l.isTerminal {
		fmt.Fprintf(os.Stderr, "[%s:ERROR] %s\n", l.tag, msg)
	}
	_ = l.writer.Err(msg)
}

func (l *unixSyslogger) Close() error {
	if l.writer != nil {
		return l.writer.Close()
	}
	return nil
}

type fallbackLogger struct {
	tag string
}

func (l *fallbackLogger) Action(user, device, cmd, ip string) {
	fmt.Fprintf(os.Stderr, "[%s:ACTION] %s\n", l.tag, formatAction(user, device, cmd, ip))
}

func (l *fallbackLogger) AuthFail(user, ip, reason string) {
	fmt.Fprintf(os.Stderr, "[%s:AUTH_FAIL] %s\n", l.tag, formatAuthFail(user, ip, reason))
}

func (l *fallbackLogger) DeviceError(device, reason string) {
	fmt.Fprintf(os.Stderr, "[%s:DEVICE_ERROR] %s\n", l.tag, formatDeviceError(device, reason))
}

func (l *fallbackLogger) Debug(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:DEBUG] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *fallbackLogger) Info(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:INFO] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *fallbackLogger) Warn(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:WARN] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *fallbackLogger) Error(format string, v ...interface{}) {
	fmt.Fprintf(os.Stderr, "[%s:ERROR] "+format+"\n", append([]interface{}{l.tag}, v...)...)
}

func (l *fallbackLogger) Close() error {
	return nil
}
