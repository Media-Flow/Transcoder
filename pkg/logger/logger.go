package logger

import (
	"Transcoder/pkg/config"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func parseLevel(s string) Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return DEBUG
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

type core struct {
	out      *log.Logger
	minLevel Level
}

var (
	sharedCore *core
	once       sync.Once
	initErr    error
)

type Logger struct {
	component string
	core      *core
}

func InitLogger(cfg *config.Config) error {
	once.Do(func() {
		var out io.Writer = os.Stdout
		if path := cfg.Logger.OutputPath; path != "" {
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
			if err != nil {
				initErr = err
			} else {
				out = io.MultiWriter(file, os.Stdout)
			}
		}

		sharedCore = &core{
			out:      log.New(out, "", 0),
			minLevel: parseLevel(cfg.Logger.Level),
		}
		if initErr != nil {
			(&Logger{component: "Logger", core: sharedCore}).
				Warnf("cannot open log file, using console only: %v", initErr)
		}
	})
	return initErr
}

func NewLogger(component string) *Logger {
	if sharedCore == nil {
		panic("logger not initialized: call InitLogger first")
	}

	return &Logger{
		component: component,
		core:      sharedCore,
	}
}

func (l *Logger) log(level Level, format string, args ...any) {
	if level < l.core.minLevel {
		return
	}
	l.core.out.Printf("[%s] [%s][%s] [%s]",
		l.component,
		time.Now().Format("2006-01-02 15:04:05"),
		level.String(),
		fmt.Sprintf(format, args...),
	)
}

func (l *Logger) Debugf(format string, args ...any) { l.log(DEBUG, format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.log(INFO, format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.log(WARN, format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.log(ERROR, format, args...) }
