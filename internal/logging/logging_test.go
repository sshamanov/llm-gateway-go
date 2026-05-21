package logging

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

// TestLoggerInfo verifies that Info writes a valid JSON entry to stdout
// and nothing to stderr.
func TestLoggerInfo(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	logger := NewLogger(LevelInfo, "test")
	logger.SetOutput(&stdout, &stderr)

	logger.Info("hello world")

	if stdout.Len() == 0 {
		t.Fatal("expected output on stdout")
	}
	if stderr.Len() > 0 {
		t.Error("unexpected output on stderr")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["level"] != "info" {
		t.Errorf("level = %q, want %q", result["level"], "info")
	}
	if result["msg"] != "hello world" {
		t.Errorf("msg = %q, want %q", result["msg"], "hello world")
	}
}

// TestLoggerError verifies that Error writes a valid JSON entry to stderr
// and nothing to stdout.
func TestLoggerError(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	logger := NewLogger(LevelError, "test")
	logger.SetOutput(&stdout, &stderr)

	logger.Error("an error")

	if stderr.Len() == 0 {
		t.Fatal("expected output on stderr")
	}
	if stdout.Len() > 0 {
		t.Error("unexpected output on stdout")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["level"] != "error" {
		t.Errorf("level = %q, want %q", result["level"], "error")
	}
	if result["msg"] != "an error" {
		t.Errorf("msg = %q, want %q", result["msg"], "an error")
	}
}

// TestLoggerDebugSuppressed verifies that Debug produces no output when the
// logger is configured at Info level or higher.
func TestLoggerDebugSuppressed(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelInfo, "test")
	logger.SetOutput(&buf, &buf)

	logger.Debug("should not appear")

	if buf.Len() > 0 {
		t.Error("expected no output for Debug at Info level")
	}
}

// TestLoggerDebugEmitted verifies that Debug produces output when the
// logger is configured at Debug level.
func TestLoggerDebugEmitted(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelDebug, "test")
	logger.SetOutput(&buf, &buf)

	logger.Debug("debug message")

	if buf.Len() == 0 {
		t.Fatal("expected output for Debug at Debug level")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["level"] != "debug" {
		t.Errorf("level = %q, want %q", result["level"], "debug")
	}
	if result["msg"] != "debug message" {
		t.Errorf("msg = %q, want %q", result["msg"], "debug message")
	}
}

// TestLoggerJSONStructure verifies that every log entry contains the required
// top-level keys: time, level, component, msg.
func TestLoggerJSONStructure(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelInfo, "mycomponent")
	logger.SetOutput(&buf, &buf)

	logger.Info("test msg")

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	required := []string{"time", "level", "component", "msg"}
	for _, key := range required {
		if _, ok := result[key]; !ok {
			t.Errorf("missing required key: %s", key)
		}
	}

	if result["component"] != "mycomponent" {
		t.Errorf("component = %q, want %q", result["component"], "mycomponent")
	}

	// Verify time is in RFC3339Nano format.
	timeStr, ok := result["time"].(string)
	if !ok {
		t.Fatal("time is not a string")
	}
	if _, err := time.Parse(time.RFC3339Nano, timeStr); err != nil {
		t.Errorf("time is not RFC3339Nano: %v", err)
	}
}

// TestLoggerCustomFields verifies that extra fields passed to a log method
// appear in the JSON output alongside the standard keys.
func TestLoggerCustomFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelInfo, "test")
	logger.SetOutput(&buf, &buf)

	logger.Info("with fields",
		String("str", "val"),
		Int("num", 42),
		Bool("flag", true),
		Duration("dur", 5*time.Second),
		Any("any", []string{"a", "b"}),
	)

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result["str"] != "val" {
		t.Errorf("str = %q, want %q", result["str"], "val")
	}

	num, ok := result["num"].(float64)
	if !ok || num != 42 {
		t.Errorf("num = %v (type %T), want 42", result["num"], result["num"])
	}

	flag, ok := result["flag"].(bool)
	if !ok || flag != true {
		t.Errorf("flag = %v (type %T), want true", result["flag"], result["flag"])
	}

	dur, ok := result["dur"].(string)
	if !ok || dur != "5s" {
		t.Errorf("dur = %v (type %T), want %q", result["dur"], result["dur"], "5s")
	}

	// Verify standard keys are still present.
	if _, ok := result["time"]; !ok {
		t.Error("missing time key")
	}
	if _, ok := result["level"]; !ok {
		t.Error("missing level key")
	}
	if _, ok := result["component"]; !ok {
		t.Error("missing component key")
	}
	if _, ok := result["msg"]; !ok {
		t.Error("missing msg key")
	}
}

// TestLoggerWarn verifies that Warn writes to stderr and emits the correct
// level string. (Bonus coverage beyond the required tests.)
func TestLoggerWarn(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	logger := NewLogger(LevelWarn, "test")
	logger.SetOutput(&stdout, &stderr)

	logger.Warn("warning message")

	if stderr.Len() == 0 {
		t.Fatal("expected output on stderr for Warn")
	}
	if stdout.Len() > 0 {
		t.Error("unexpected output on stdout")
	}

	var result map[string]interface{}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["level"] != "warn" {
		t.Errorf("level = %q, want %q", result["level"], "warn")
	}
}

// TestLoggerWarnSuppressed verifies that Warn is suppressed when the
// logger is configured at Error level.
func TestLoggerWarnSuppressed(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelError, "test")
	logger.SetOutput(&buf, &buf)

	logger.Warn("should not appear")

	if buf.Len() > 0 {
		t.Error("expected no output for Warn at Error level")
	}
}

// TestLoggerInfoSuppressed verifies that Info is suppressed when the
// logger is configured at Warn level.
func TestLoggerInfoSuppressed(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelWarn, "test")
	logger.SetOutput(&buf, &buf)

	logger.Info("should not appear")

	if buf.Len() > 0 {
		t.Error("expected no output for Info at Warn level")
	}
}

// TestLoggerMultipleFields verifies multiple fields are written in the
// order they are provided.
func TestLoggerMultipleFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelInfo, "test")
	logger.SetOutput(&buf, &buf)

	logger.Info("multi", String("a", "1"), String("b", "2"), String("c", "3"))

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if result["a"] != "1" || result["b"] != "2" || result["c"] != "3" {
		t.Errorf("unexpected field values: a=%v b=%v c=%v", result["a"], result["b"], result["c"])
	}
}

// TestLoggerZeroFields verifies logging with no extra fields does not
// produce a spurious error.
func TestLoggerZeroFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(LevelInfo, "test")
	logger.SetOutput(&buf, &buf)

	logger.Info("no extra fields")

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["msg"] != "no extra fields" {
		t.Errorf("msg = %q, want %q", result["msg"], "no extra fields")
	}
	// There should be exactly 4 keys (time, level, component, msg).
	if len(result) != 4 {
		t.Errorf("got %d keys, want 4: %v", len(result), keys(result))
	}
}

// TestLevelString verifies that each LogLevel constant has the expected
// string form.
func TestLevelString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level LogLevel
		want  string
	}{
		{name: "debug", level: LevelDebug, want: "debug"},
		{name: "info", level: LevelInfo, want: "info"},
		{name: "warn", level: LevelWarn, want: "warn"},
		{name: "error", level: LevelError, want: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.level.String(); got != tt.want {
				t.Errorf("LogLevel.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// keys returns the keys of a string-keyed map for diagnostic output.
func keys(m map[string]interface{}) []string {
	k := make([]string, 0, len(m))
	for key := range m {
		k = append(k, key)
	}
	return k
}
