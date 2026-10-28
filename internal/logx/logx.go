// Package logx implements leveled logging with an in-memory ring buffer and
// subscriber callbacks used by the CLI and the web UI.
package logx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warning
	Error
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "DEBUG"
	case Info:
		return "INFO"
	case Warning:
		return "WARNING"
	case Error:
		return "ERROR"
	default:
		return "INFO"
	}
}

func parseLevel(s string) Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return Debug
	case "WARNING", "WARN":
		return Warning
	case "ERROR":
		return Error
	default:
		return Info
	}
}

// Line is one buffered log record.
type Line struct {
	Index int    `json:"index"`
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

type Subscriber func(ts, level, msg string)

var (
	mu          sync.Mutex
	minLevel    = Info
	history     []Line
	historySize = 2000
	nextIndex   int
	subs        []Subscriber
	out         io.Writer = os.Stdout
)

// Setup configures the global logger. It is safe to call more than once.
func Setup(level string, historyLen int) {
	mu.Lock()
	defer mu.Unlock()
	minLevel = parseLevel(level)
	if historyLen > 0 {
		historySize = historyLen
	}
	if history == nil {
		history = make([]Line, 0, historySize)
	}
}

// SetOutput redirects the console output (useful in tests).
func SetOutput(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	out = w
}

func emit(lv Level, name, format string, args ...any) {
	mu.Lock()
	if lv < minLevel {
		mu.Unlock()
		return
	}
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("15:04:05")
	full := msg
	if name != "" {
		full = name + ": " + msg
	}
	line := Line{Index: nextIndex, Time: ts, Level: lv.String(), Msg: full}
	nextIndex++
	history = append(history, line)
	if len(history) > historySize {
		history = history[len(history)-historySize:]
	}
	snapshot := append([]Subscriber(nil), subs...)
	w := out
	mu.Unlock()

	fmt.Fprintf(w, "%s %s %s\n", ts, lv.String(), full)
	for _, cb := range snapshot {
		func() {
			defer func() { _ = recover() }()
			cb(ts, lv.String(), msg)
		}()
	}
}

// Logger is a named logger.
type Logger struct{ name string }

func GetLogger(name string) *Logger { return &Logger{name: name} }

func (l *Logger) Debugf(format string, args ...any)   { emit(Debug, l.name, format, args...) }
func (l *Logger) Infof(format string, args ...any)    { emit(Info, l.name, format, args...) }
func (l *Logger) Warningf(format string, args ...any) { emit(Warning, l.name, format, args...) }
func (l *Logger) Errorf(format string, args ...any)   { emit(Error, l.name, format, args...) }

// RingHistory returns a copy of the buffered log lines.
func RingHistory() []Line {
	mu.Lock()
	defer mu.Unlock()
	return append([]Line(nil), history...)
}

// RingSince returns lines with Index >= since.
func RingSince(since int) []Line {
	mu.Lock()
	defer mu.Unlock()
	for i, ln := range history {
		if ln.Index >= since {
			return append([]Line(nil), history[i:]...)
		}
	}
	return nil
}

// Subscribe registers a callback for every record emitted from now on.
func Subscribe(cb Subscriber) {
	mu.Lock()
	defer mu.Unlock()
	subs = append(subs, cb)
}
