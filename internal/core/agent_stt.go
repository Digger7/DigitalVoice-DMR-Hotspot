package core

import (
	"bufio"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"dmr-hotspot/internal/logx"
)

//go:embed stt_worker.py
var sttWorkerSource string

// WhisperSTT runs faster-whisper through a persistent Python worker so the
// model is loaded only once.
type WhisperSTT struct {
	Model    string
	Language string
	Device   string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *stderrTail
	script string
}

// stderrTail keeps the last few KB of worker stderr so a crashed interpreter
// can be reported instead of a bare "broken pipe".
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (s *stderrTail) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if len(s.buf) > 4096 {
		s.buf = append([]byte(nil), s.buf[len(s.buf)-4096:]...)
	}
	return len(p), nil
}

func (s *stderrTail) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.buf))
}

func NewWhisperSTT(model, language, device string) *WhisperSTT {
	return &WhisperSTT{Model: model, Language: language, Device: device}
}

func findPython() (string, error) {
	if p := os.Getenv("DMR_PYTHON"); p != "" {
		return p, nil
	}
	var candidates []string
	for _, root := range searchRoots() {
		candidates = append(candidates,
			filepath.Join(root, ".venv", "bin", "python"),
		)
	}
	candidates = append(candidates, "python3", "python")
	for _, c := range candidates {
		if strings.ContainsRune(c, os.PathSeparator) || filepath.IsAbs(c) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c, nil
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python interpreter not found (set DMR_PYTHON)")
}

func (w *WhisperSTT) start() error {
	if w.cmd != nil {
		return nil
	}
	python, err := findPython()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "dmr-stt-*.py")
	if err != nil {
		return err
	}
	if _, err := f.WriteString(sttWorkerSource); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	w.script = f.Name()
	w.stderr = &stderrTail{}
	cmd := exec.Command(python, "-u", w.script, w.Model, w.Language, w.Device)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.MultiWriter(os.Stderr, w.stderr)
	if err := cmd.Start(); err != nil {
		return err
	}
	w.cmd = cmd
	w.stdin = stdin
	w.stdout = bufio.NewReaderSize(stdout, 1<<20)
	return nil
}

func (w *WhisperSTT) fail(err error) error {
	msg := ""
	if w.stderr != nil {
		msg = w.stderr.String()
	}
	w.reset()
	if msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

func (w *WhisperSTT) reset() {
	if w.cmd != nil {
		if w.stdin != nil {
			_ = w.stdin.Close()
		}
		if w.cmd.Process != nil {
			_ = w.cmd.Process.Kill()
		}
		_ = w.cmd.Wait()
	}
	if w.script != "" {
		_ = os.Remove(w.script)
	}
	w.cmd = nil
	w.stdin = nil
	w.stdout = nil
	w.stderr = nil
	w.script = ""
}

func (w *WhisperSTT) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reset()
}

// Transcribe returns the recognised text for 16 kHz mono s16le PCM.
func (w *WhisperSTT) Transcribe(pcm16k []byte) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.start(); err != nil {
		return "", err
	}
	req, err := json.Marshal(map[string]string{
		"pcm": base64.StdEncoding.EncodeToString(pcm16k),
	})
	if err != nil {
		return "", err
	}
	if _, err := w.stdin.Write(append(req, '\n')); err != nil {
		return "", w.fail(err)
	}
	line, err := w.stdout.ReadBytes('\n')
	if err != nil {
		return "", w.fail(err)
	}
	var resp struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		w.reset()
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	logx.GetLogger("agent").Debugf("STT: %s", resp.Text)
	return resp.Text, nil
}
