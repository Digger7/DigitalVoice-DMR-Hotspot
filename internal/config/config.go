// Package config defines the configuration model and YAML load/save helpers.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"dmr-hotspot/internal/protocol"
)

type ModemConfig struct {
	Port      string `yaml:"port" json:"port"`
	Baud      int    `yaml:"baud" json:"baud"`
	Duplex    bool   `yaml:"duplex" json:"duplex"`
	RXInvert  bool   `yaml:"rx_invert" json:"rx_invert"`
	TXInvert  bool   `yaml:"tx_invert" json:"tx_invert"`
	PTTInvert bool   `yaml:"ptt_invert" json:"ptt_invert"`
	TXDelay   int    `yaml:"tx_delay" json:"tx_delay"`
	DMRDelay  int    `yaml:"dmr_delay" json:"dmr_delay"`
	Debug     bool   `yaml:"debug" json:"debug"`
}

type RFConfig struct {
	RXFrequency int `yaml:"rx_frequency" json:"rx_frequency"`
	TXFrequency int `yaml:"tx_frequency" json:"tx_frequency"`
	RXOffset    int `yaml:"rx_offset" json:"rx_offset"`
	TXOffset    int `yaml:"tx_offset" json:"tx_offset"`
	RFLevel     int `yaml:"rf_level" json:"rf_level"`
	ColorCode   int `yaml:"color_code" json:"color_code"`
	TXDCOffset  int `yaml:"tx_dc_offset" json:"tx_dc_offset"`
	RXDCOffset  int `yaml:"rx_dc_offset" json:"rx_dc_offset"`
	TXGainDB    int `yaml:"tx_gain_db" json:"tx_gain_db"`
}

type NetworkConfig struct {
	Address   string        `yaml:"address" json:"address"`
	Port      int           `yaml:"port" json:"port"`
	Password  string        `yaml:"password" json:"password"`
	Name      string        `yaml:"name" json:"name"`
	Location  bool          `yaml:"location" json:"location"`
	Slot1     bool          `yaml:"slot1" json:"slot1"`
	Slot2     bool          `yaml:"slot2" json:"slot2"`
	StaticTGs map[int][]int `yaml:"static_tgs" json:"static_tgs"`
	Hang      int           `yaml:"hang" json:"hang"`
}

type StationConfig struct {
	Callsign string `yaml:"callsign" json:"callsign"`
	DMRID    int    `yaml:"dmr_id" json:"dmr_id"`
}

type AgentConfig struct {
	Enabled             bool    `yaml:"enabled" json:"enabled"`
	STTEngine           string  `yaml:"stt_engine" json:"stt_engine"`
	WhisperModel        string  `yaml:"whisper_model" json:"whisper_model"`
	WhisperLanguage     string  `yaml:"whisper_language" json:"whisper_language"`
	WhisperDevice       string  `yaml:"whisper_device" json:"whisper_device"`
	LLMProvider         string  `yaml:"llm_provider" json:"llm_provider"`
	LLMBaseURL          string  `yaml:"llm_base_url" json:"llm_base_url"`
	LLMModel            string  `yaml:"llm_model" json:"llm_model"`
	SystemPrompt        string  `yaml:"system_prompt" json:"system_prompt"`
	LLMAPIKey           string  `yaml:"llm_api_key" json:"llm_api_key"`
	LLMTemperature      float64 `yaml:"llm_temperature" json:"llm_temperature"`
	LLMMaxTokens        int     `yaml:"llm_max_tokens" json:"llm_max_tokens"`
	LLMTimeout          float64 `yaml:"llm_timeout" json:"llm_timeout"`
	WebSearch           bool    `yaml:"web_search" json:"web_search"`
	WebSearchProvider   string  `yaml:"web_search_provider" json:"web_search_provider"`
	WebSearchBaseURL    string  `yaml:"web_search_base_url" json:"web_search_base_url"`
	WebSearchLanguage   string  `yaml:"web_search_language" json:"web_search_language"`
	WebSearchMaxResults int     `yaml:"web_search_max_results" json:"web_search_max_results"`
	WebSearchFetchPages int     `yaml:"web_search_fetch_pages" json:"web_search_fetch_pages"`
	WebSearchTimeout    float64 `yaml:"web_search_timeout" json:"web_search_timeout"`
	TTSEngine           string  `yaml:"tts_engine" json:"tts_engine"`
	PiperVoiceDir       string  `yaml:"piper_voice_dir" json:"piper_voice_dir"`
	PiperVoice          string  `yaml:"piper_voice" json:"piper_voice"`
	PiperModelPath      string  `yaml:"piper_model_path" json:"piper_model_path"`
	TTSVolume           float64 `yaml:"tts_volume" json:"tts_volume"`
	TTSLengthScale      float64 `yaml:"tts_length_scale" json:"tts_length_scale"`
	TTSRate             float64 `yaml:"tts_rate" json:"tts_rate"`
	ReplySrcID          int     `yaml:"reply_src_id" json:"reply_src_id"`
	ReplyDstID          int     `yaml:"reply_dst_id" json:"reply_dst_id"`
	ReplyFLCO           int     `yaml:"reply_flco" json:"reply_flco"`
	HoldTimeout         float64 `yaml:"hold_timeout" json:"hold_timeout"`
	MaxQueue            int     `yaml:"max_queue" json:"max_queue"`
}

type AppConfig struct {
	Modem    ModemConfig   `yaml:"modem" json:"modem"`
	RF       RFConfig      `yaml:"rf" json:"rf"`
	Network  NetworkConfig `yaml:"network" json:"network"`
	Station  StationConfig `yaml:"station" json:"station"`
	Agent    AgentConfig   `yaml:"agent" json:"agent"`
	Mode     string        `yaml:"mode" json:"mode"`
	LogLevel string        `yaml:"log_level" json:"log_level"`
}

// Default returns the same defaults as the Python application.
func Default() AppConfig {
	return AppConfig{
		Modem: ModemConfig{
			Port:    "",
			Baud:    115200,
			TXDelay: 100,
		},
		RF: RFConfig{
			RXFrequency: 438800000,
			TXFrequency: 438800000,
			RFLevel:     20,
			ColorCode:   1,
			TXGainDB:    0,
		},
		Network: NetworkConfig{
			Address:   "2502.master.brandmeister.network",
			Port:      protocol.BMDefaultPort,
			Name:      "BM",
			Slot2:     true,
			StaticTGs: map[int][]int{2: {9}},
			Hang:      30,
		},
		Station: StationConfig{Callsign: "N0CALL"},
		Agent: AgentConfig{
			Enabled:             false,
			STTEngine:           "faster-whisper",
			WhisperModel:        "small",
			WhisperLanguage:     "ru",
			WhisperDevice:       "auto",
			LLMProvider:         LLMProviderCustom,
			LLMBaseURL:          "http://localhost:1234",
			SystemPrompt:        "Ты — голосовой радиолюбительский ассистент. Отвечай кратко, чётко, по-русски, одним-двумя короткими предложениями. Без списков, markdown, эмодзи и лишних символов.",
			LLMTemperature:      0.4,
			LLMMaxTokens:        1024,
			LLMTimeout:          60.0,
			WebSearch:           false,
			WebSearchProvider:   "duckduckgo",
			WebSearchLanguage:   "ru",
			WebSearchMaxResults: 5,
			WebSearchFetchPages: 2,
			WebSearchTimeout:    12.0,
			TTSEngine:           "piper",
			TTSVolume:           1.0,
			TTSLengthScale:      1.12,
			TTSRate:             1.0,
			ReplyFLCO:           0,
			HoldTimeout:         1.5,
			MaxQueue:            300,
		},
		Mode:     "network",
		LogLevel: "INFO",
	}
}

func (c AppConfig) Validate() []string {
	var errors []string
	switch c.Mode {
	case "network", "echo", "agent":
	default:
		errors = append(errors, fmt.Sprintf("mode must be 'network', 'echo' or 'agent', got '%s'", c.Mode))
	}
	if c.Mode == "network" && c.Station.DMRID <= 1000 {
		errors = append(errors, "station.dmr_id must be your DMR ID (e.g. 2501234)")
	}
	if strings.TrimSpace(c.Station.Callsign) == "" {
		errors = append(errors, "station.callsign is empty")
	}
	if c.RF.ColorCode < 0 || c.RF.ColorCode > 15 {
		errors = append(errors, "rf.color_code must be in 0..15")
	}
	if c.RF.RFLevel <= 0 || c.RF.RFLevel > 100 {
		errors = append(errors, "rf.rf_level must be in 1..100")
	}
	if c.RF.TXGainDB < -20 || c.RF.TXGainDB > 20 {
		errors = append(errors, "rf.tx_gain_db must be in -20..20 dB")
	}
	if c.Agent.TTSVolume <= 0 || c.Agent.TTSVolume > 1.2 {
		errors = append(errors, "agent.tts_volume must be in 0..1.2 (1.0 = clean)")
	}
	if c.Agent.TTSRate != 0 && (c.Agent.TTSRate < 0.5 || c.Agent.TTSRate > 2.0) {
		errors = append(errors, "agent.tts_rate must be in 0.5..2.0 (1.0 = normal speed)")
	}
	if c.Agent.LLMProvider != "" {
		if p, ok := LookupLLMProvider(c.Agent.LLMProvider); !ok {
			errors = append(errors, fmt.Sprintf("agent.llm_provider is unknown: '%s'", c.Agent.LLMProvider))
		} else if p.RequiresKey && strings.TrimSpace(c.Agent.LLMAPIKey) == "" {
			errors = append(errors, fmt.Sprintf("agent.llm_api_key is required for provider '%s'", p.ID))
		}
	}
	if c.Agent.WebSearch {
		switch strings.ToLower(strings.TrimSpace(c.Agent.WebSearchProvider)) {
		case "", "duckduckgo", "searxng":
		default:
			errors = append(errors, fmt.Sprintf("agent.web_search_provider must be 'duckduckgo' or 'searxng', got '%s'", c.Agent.WebSearchProvider))
		}
		if strings.EqualFold(strings.TrimSpace(c.Agent.WebSearchProvider), "searxng") &&
			strings.TrimSpace(c.Agent.WebSearchBaseURL) == "" {
			errors = append(errors, "agent.web_search_base_url is required for SearXNG")
		}
		if c.Agent.WebSearchMaxResults < 1 || c.Agent.WebSearchMaxResults > 10 {
			errors = append(errors, "agent.web_search_max_results must be in 1..10")
		}
		if c.Agent.WebSearchFetchPages < 0 || c.Agent.WebSearchFetchPages > 5 {
			errors = append(errors, "agent.web_search_fetch_pages must be in 0..5")
		}
		if c.Agent.WebSearchTimeout <= 0 || c.Agent.WebSearchTimeout > 60 {
			errors = append(errors, "agent.web_search_timeout must be in 1..60 seconds")
		}
	}
	return errors
}

// LoadFile reads a YAML config, overlaying it on the defaults.
func LoadFile(path string) (AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, err
	}
	return LoadBytes(data)
}

func LoadBytes(data []byte) (AppConfig, error) {
	cfg := Default()
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, err
		}
	}
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = "network"
	}
	cfg.Agent.LLMProvider = strings.ToLower(strings.TrimSpace(cfg.Agent.LLMProvider))
	if cfg.Agent.LLMProvider == "" {
		cfg.Agent.LLMProvider = LLMProviderCustom
	}
	cfg.Agent.WebSearchProvider = strings.ToLower(strings.TrimSpace(cfg.Agent.WebSearchProvider))
	if cfg.Agent.WebSearchProvider == "" {
		cfg.Agent.WebSearchProvider = "duckduckgo"
	}
	cfg.Agent.WebSearchBaseURL = strings.TrimSpace(cfg.Agent.WebSearchBaseURL)
	cfg.Agent.WebSearchLanguage = strings.TrimSpace(cfg.Agent.WebSearchLanguage)
	return cfg, nil
}

func SaveFile(cfg AppConfig, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadOrInit loads path, creating it from the defaults when it does not exist.
func LoadOrInit(path string) (AppConfig, error) {
	cfg, err := LoadFile(path)
	if err == nil {
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		return cfg, err
	}
	cfg = Default()
	if err := SaveFile(cfg, path); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// DefaultConfigFile returns a writable per-user config path.
func DefaultConfigFile() string {
	home, _ := os.UserHomeDir()
	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support", "DMR Hotspot")
	case "windows":
		appdata := os.Getenv("APPDATA")
		if appdata == "" {
			appdata = home
		}
		base = filepath.Join(appdata, "DMR Hotspot")
	default:
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		base = filepath.Join(xdg, "dmr-hotspot")
	}
	_ = os.MkdirAll(base, 0o755)
	return filepath.Join(base, "config.yaml")
}
