// Package webui exposes a small local HTTP UI and JSON API for the hotspot.
package webui

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/core"
	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/modem"
)

var log = logx.GetLogger("webui")

type Server struct {
	mu      sync.Mutex
	cfg     config.AppConfig
	cfgPath string
	core    *core.Core
}

func New(cfg config.AppConfig, cfgPath string) *Server {
	return &Server{cfg: cfg, cfgPath: cfgPath}
}

func (s *Server) ListenAndServe(addr string) error {
	log.Infof("Web UI: http://%s", addr)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) startLocked() error {
	if s.core != nil && s.core.Running() {
		return fmt.Errorf("already running")
	}
	c := core.NewCore(s.cfg)
	s.core = c
	return c.Start()
}

func (s *Server) stopLocked() {
	if s.core != nil {
		c := s.core
		s.core = nil
		c.Stop()
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/icon.png", s.handleIcon)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/restart", s.handleRestart)
	mux.HandleFunc("/api/ports", s.handlePorts)
	mux.HandleFunc("/api/voices", s.handleVoices)
	mux.HandleFunc("/api/llm-providers", s.handleLLMProviders)
	mux.HandleFunc("/api/test-llm", s.handleTestLLM)
	mux.HandleFunc("/api/test-tts", s.handleTestTTS)
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(indexHTML))
}

func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=86400")
	_, _ = w.Write(iconPNG)
}

type statusPayload struct {
	Status     core.RuntimeStatus    `json:"status"`
	Running    bool                  `json:"running"`
	Mode       string                `json:"mode"`
	LastHeard  []core.LastHeardEntry `json:"last_heard"`
	Echo       *core.EchoStats       `json:"echo,omitempty"`
	Agent      *core.AgentStats      `json:"agent,omitempty"`
	ConfigPath string                `json:"config_path"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	c := s.core
	cfg := s.cfg
	path := s.cfgPath
	s.mu.Unlock()

	payload := statusPayload{Mode: cfg.Mode, ConfigPath: path, Running: c != nil && c.Running()}
	if c == nil {
		payload.Status.State = "STOPPED"
	} else {
		payload.Status = c.Status()
		payload.LastHeard = c.LastHeard()
		if e := c.Echo(); e != nil {
			st := e.Stats()
			payload.Echo = &st
		}
		if a := c.Agent(); a != nil {
			st := a.Stats()
			payload.Agent = &st
		}
	}
	writeJSON(w, payload)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	since := 0
	if v := r.URL.Query().Get("since"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			since = n
		}
	}
	writeJSON(w, logx.RingSince(since))
}

type configView struct {
	Config      config.AppConfig `json:"config"`
	PasswordSet bool             `json:"password_set"`
	LLMKeySet   bool             `json:"llm_key_set"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.mu.Lock()
		cfg := s.cfg
		s.mu.Unlock()
		view := configView{Config: cfg, PasswordSet: cfg.Network.Password != "",
			LLMKeySet: cfg.Agent.LLMAPIKey != ""}
		view.Config.Network.Password = ""
		view.Config.Agent.LLMAPIKey = ""
		writeJSON(w, view)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var incoming config.AppConfig
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if incoming.Network.Password == "" {
		incoming.Network.Password = s.cfg.Network.Password
	}
	if incoming.Agent.LLMAPIKey == "" {
		incoming.Agent.LLMAPIKey = s.cfg.Agent.LLMAPIKey
	}
	if errs := incoming.Validate(); len(errs) > 0 {
		s.mu.Unlock()
		writeJSONStatus(w, http.StatusBadRequest, map[string]any{"errors": errs})
		return
	}
	wasRunning := s.core != nil && s.core.Running()
	s.stopLocked()
	s.cfg = incoming
	err := config.SaveFile(incoming, s.cfgPath)
	if err == nil && wasRunning {
		err = s.startLocked()
	}
	s.mu.Unlock()
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	err := s.startLocked()
	s.mu.Unlock()
	if err != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	s.stopLocked()
	err := s.startLocked()
	s.mu.Unlock()
	if err != nil {
		writeJSONStatus(w, http.StatusConflict, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handlePorts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, modem.ListSerialPorts())
}

func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	dir := s.cfg.Agent.PiperVoiceDir
	s.mu.Unlock()
	writeJSON(w, core.ListPiperVoices(dir))
}

func (s *Server) handleTestTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var a config.AgentConfig
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(r.URL.Query().Get("text"))
	if text == "" {
		text = "Привет! Это голос агента. Раз, два, три. Как слышно?"
	}
	tts := core.NewPiperTTS(a.PiperVoice, "", a.PiperVoiceDir, a.TTSLengthScale, a.TTSRate)
	if a.PiperModelPath != "" {
		tts.Voice = a.PiperModelPath
	}
	pcm, err := tts.Speak(text)
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	pcm = core.PolishSpeech(pcm, a.TTSVolume)
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pcmToWAV(pcm, 8000))
}

func pcmToWAV(pcm []byte, sampleRate int) []byte {
	const channels, bits = 1, 16
	out := make([]byte, 44+len(pcm))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(pcm)))
	copy(out[8:], "WAVE")
	copy(out[12:], "fmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], channels)
	binary.LittleEndian.PutUint32(out[24:], uint32(sampleRate))
	binary.LittleEndian.PutUint32(out[28:], uint32(sampleRate*channels*bits/8))
	binary.LittleEndian.PutUint16(out[32:], uint16(channels*bits/8))
	binary.LittleEndian.PutUint16(out[34:], bits)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out
}

func (s *Server) handleLLMProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"providers": config.LLMProviders()})
}

func (s *Server) handleTestLLM(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var a config.AgentConfig
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if a.LLMAPIKey == "" {
		s.mu.Lock()
		a.LLMAPIKey = s.cfg.Agent.LLMAPIKey
		s.mu.Unlock()
	}
	baseURL, model := a.ResolveLLM()
	if baseURL == "" {
		writeJSON(w, map[string]any{"ok": false, "detail": "LLM base URL is empty"})
		return
	}
	timeout := time.Duration(a.LLMTimeout * float64(time.Second))
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	llm := core.NewLMStudio(baseURL, model, a.SystemPrompt, a.LLMAPIKey,
		a.LLMTemperature, a.LLMMaxTokens, timeout)
	ok, detail := llm.TestConnection(5 * time.Second)
	writeJSON(w, map[string]any{"ok": ok, "detail": detail})
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Start starts the hotspot with the current configuration.
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startLocked()
}
