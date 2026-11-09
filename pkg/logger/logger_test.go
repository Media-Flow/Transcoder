package logger

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestLoggerRespectsMinimumLevel(t *testing.T) {
	var output bytes.Buffer
	logger := &Logger{
		component: "test",
		core: &core{
			out:      log.New(&output, "", 0),
			minLevel: INFO,
		},
	}

	logger.Debugf("debug message")
	logger.Infof("info message")
	logger.Warnf("warning message")
	logger.Errorf("error message")

	got := output.String()
	if strings.Contains(got, "debug message") {
		t.Fatalf("debug message was logged at INFO level: %q", got)
	}
	for _, message := range []string{"info message", "warning message", "error message"} {
		if !strings.Contains(got, message) {
			t.Errorf("expected %q in log output: %q", message, got)
		}
	}
}
