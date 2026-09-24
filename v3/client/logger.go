package client

import (
	"context"
	"fmt"
	"log"
	"os"
)

// Logger construct a basic interface for logger
type Logger interface {
	Debug(context.Context, ...any)
	Info(context.Context, ...any)
	Warn(context.Context, ...any)
	Error(context.Context, ...any)
}

// NewLogger creates a new logger
func NewLogger() Logger {
	logger := defaultLogger{
		logger: log.New(os.Stdout, "", log.LstdFlags),
	}
	return logger
}

// defaultLogger is a sets of default internal logger methods
type defaultLogger struct {
	logger *log.Logger
}

// Debug build Debug level log
func (l defaultLogger) Debug(ctx context.Context, args ...any) {
	l.logger.Printf("[Debug] %s", fmt.Sprint(args...))
}

// Info build Info level log
func (l defaultLogger) Info(ctx context.Context, args ...any) {
	l.logger.Printf("[Info] %s", fmt.Sprint(args...))
}

// Warn build Warn level log
func (l defaultLogger) Warn(ctx context.Context, args ...any) {
	l.logger.Printf("[Warn] %s", fmt.Sprint(args...))
}

// Error build Error level log
func (l defaultLogger) Error(ctx context.Context, args ...any) {
	l.logger.Printf("[Error] %s", fmt.Sprint(args...))
}

// restyLogger routes resty's own diagnostics through the SDK Logger instead of
// letting the library write to stderr behind WithLogger's back
type restyLogger struct {
	Logger
}

func (l *restyLogger) Errorf(format string, args ...any) {
	l.Logger.Error(nil, fmt.Sprintf(format, args...))
}

func (l *restyLogger) Warnf(format string, args ...any) {
	l.Logger.Warn(nil, fmt.Sprintf(format, args...))
}

func (l *restyLogger) Debugf(format string, args ...any) {
	l.Logger.Debug(nil, fmt.Sprintf(format, args...))
}
