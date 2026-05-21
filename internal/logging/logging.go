package logging

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

// LogLevel represents the severity of a log message.
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the lowercase string representation of the log level.
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "unknown"
	}
}

// Field represents a structured key-value pair in a log entry.
type Field struct {
	Key   string
	Value interface{}
}

// String creates a Field with a string value.
func String(key, value string) Field {
	return Field{Key: key, Value: value}
}

// Int creates a Field with an int value.
func Int(key string, value int) Field {
	return Field{Key: key, Value: value}
}

// Bool creates a Field with a bool value.
func Bool(key string, value bool) Field {
	return Field{Key: key, Value: value}
}

// Duration creates a Field with a time.Duration value. The value is
// serialized as a human-readable string (e.g. "5s") in JSON output.
func Duration(key string, value time.Duration) Field {
	return Field{Key: key, Value: value}
}

// Any creates a Field with an arbitrary value.
func Any(key string, value interface{}) Field {
	return Field{Key: key, Value: value}
}

// Logger provides structured JSON logging at multiple severity levels.
// Debug and Info messages are written to stdout; Warn and Error messages
// are written to stderr.
type Logger struct {
	level     LogLevel
	component string
	stdout    io.Writer
	stderr    io.Writer
}

// NewLogger creates a new Logger with the given severity threshold and
// component name. Output defaults to os.Stdout and os.Stderr.
func NewLogger(level LogLevel, component string) *Logger {
	return &Logger{
		level:     level,
		component: component,
		stdout:    os.Stdout,
		stderr:    os.Stderr,
	}
}

// SetOutput replaces the output writers. Useful in tests to capture output.
func (l *Logger) SetOutput(stdout, stderr io.Writer) {
	l.stdout = stdout
	l.stderr = stderr
}

// shouldEmit reports whether a message at the given level should be
// emitted based on the Logger's configured threshold.
func (l *Logger) shouldEmit(level LogLevel) bool {
	return level >= l.level
}

// writer returns the appropriate output writer for the given level.
func (l *Logger) writer(level LogLevel) io.Writer {
	if level >= LevelWarn {
		return l.stderr
	}
	return l.stdout
}

// log constructs and writes a structured JSON log entry.
func (l *Logger) log(level LogLevel, msg string, fields ...Field) {
	if !l.shouldEmit(level) {
		return
	}

	entry := map[string]interface{}{
		"time":      time.Now().UTC().Format(time.RFC3339Nano),
		"level":     level.String(),
		"component": l.component,
		"msg":       msg,
	}

	for _, f := range fields {
		switch v := f.Value.(type) {
		case time.Duration:
			entry[f.Key] = v.String()
		default:
			entry[f.Key] = v
		}
	}

	enc := json.NewEncoder(l.writer(level))
	enc.Encode(entry)
}

// Debug logs a message at debug level.
func (l *Logger) Debug(msg string, fields ...Field) {
	l.log(LevelDebug, msg, fields...)
}

// Info logs a message at info level.
func (l *Logger) Info(msg string, fields ...Field) {
	l.log(LevelInfo, msg, fields...)
}

// Warn logs a message at warn level.
func (l *Logger) Warn(msg string, fields ...Field) {
	l.log(LevelWarn, msg, fields...)
}

// Error logs a message at error level.
func (l *Logger) Error(msg string, fields ...Field) {
	l.log(LevelError, msg, fields...)
}
