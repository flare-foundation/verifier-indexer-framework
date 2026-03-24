package logger

import (
	commonlogger "github.com/flare-foundation/go-flare-common/pkg/logger"
)

// Logger defines the logging operations accepted by the framework packages.
type Logger interface {
	Debug(args ...any)
	Debugf(msg string, args ...any)
	Info(args ...any)
	Infof(msg string, args ...any)
	Warn(args ...any)
	Warnf(msg string, args ...any)
	Error(args ...any)
	Errorf(msg string, args ...any)
}

// Adapter wraps the go-flare-common package-level logger functions to satisfy
// the Logger interface.
type Adapter struct{}

var _ Logger = Adapter{}

// Debug logs arguments at DEBUG level.
func (Adapter) Debug(args ...any) { commonlogger.Debug(args...) }

// Debugf formats the message and logs it at DEBUG level.
func (Adapter) Debugf(msg string, args ...any) { commonlogger.Debugf(msg, args...) }

// Info logs arguments at INFO level.
func (Adapter) Info(args ...any) { commonlogger.Info(args...) }

// Infof formats the message and logs it at INFO level.
func (Adapter) Infof(msg string, args ...any) { commonlogger.Infof(msg, args...) }

// Warn logs arguments at WARN level.
func (Adapter) Warn(args ...any) { commonlogger.Warn(args...) }

// Warnf formats the message and logs it at WARN level.
func (Adapter) Warnf(msg string, args ...any) { commonlogger.Warnf(msg, args...) }

// Error logs arguments at ERROR level.
func (Adapter) Error(args ...any) { commonlogger.Error(args...) }

// Errorf formats the message and logs it at ERROR level.
func (Adapter) Errorf(msg string, args ...any) { commonlogger.Errorf(msg, args...) }

// Nop is a Logger that discards all log output.
type Nop struct{}

var _ Logger = Nop{}

// Debug is a no-op.
func (Nop) Debug(...any) {}

// Debugf is a no-op.
func (Nop) Debugf(string, ...any) {}

// Info is a no-op.
func (Nop) Info(...any) {}

// Infof is a no-op.
func (Nop) Infof(string, ...any) {}

// Warn is a no-op.
func (Nop) Warn(...any) {}

// Warnf is a no-op.
func (Nop) Warnf(string, ...any) {}

// Error is a no-op.
func (Nop) Error(...any) {}

// Errorf is a no-op.
func (Nop) Errorf(string, ...any) {}
