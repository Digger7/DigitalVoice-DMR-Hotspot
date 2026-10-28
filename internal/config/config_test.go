package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.Mode != "network" || c.RF.ColorCode != 1 || c.RF.RXFrequency != 438800000 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.Network.Port == 0 || c.Network.StaticTGs[2][0] != 9 {
		t.Fatalf("network defaults: %+v", c.Network)
	}
	if c.Agent.TTSRate != 1.0 {
		t.Fatalf("tts_rate default = %v, want 1.0", c.Agent.TTSRate)
	}
	if errs := c.Validate(); len(errs) != 1 {
		t.Fatalf("expected only the missing DMR ID, got %v", errs)
	}
}

func TestLoadPartialPreservesDefaults(t *testing.T) {
	c, err := LoadBytes([]byte("station:\n  callsign: N0CALL\n  dmr_id: 2501234\nmode: echo\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.RF.ColorCode != 1 || c.Modem.Baud != 115200 {
		t.Fatalf("defaults lost: %+v", c)
	}
	if c.Station.DMRID != 2501234 || c.Mode != "echo" {
		t.Fatalf("override lost: %+v", c)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	c := Default()
	c.Station.Callsign = "N0CALL"
	c.Station.DMRID = 2501234
	c.Network.Password = "secret"
	c.RF.TXDCOffset = 12
	c.RF.RXDCOffset = -7
	c.Agent.PiperVoice = "ru_RU-denis-medium"

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := SaveFile(c, path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Network.Password != "secret" || got.RF.TXDCOffset != 12 || got.RF.RXDCOffset != -7 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	if got.Agent.PiperVoice != "ru_RU-denis-medium" {
		t.Fatalf("agent voice lost")
	}
}

func TestLoadOrInitCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	c, err := LoadOrInit(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode != "network" {
		t.Fatalf("unexpected config: %+v", c)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("config file not created")
	}
}

func TestValidationRejectsBadValues(t *testing.T) {
	c := Default()
	c.Mode = "agent"
	c.RF.ColorCode = 16
	c.RF.RFLevel = 0
	c.RF.TXGainDB = 31
	c.Agent.TTSVolume = 2
	c.Agent.TTSRate = 3
	if errs := c.Validate(); len(errs) < 5 {
		t.Fatalf("expected validation errors, got %v", errs)
	}
}

func TestLLMProviderResolve(t *testing.T) {
	a := Default().Agent
	if base, model := a.ResolveLLM(); base != "http://localhost:1234" || model != "" {
		t.Fatalf("custom defaults resolved to %q, %q", base, model)
	}

	a.LLMProvider = LLMProviderGroq
	a.LLMBaseURL = "http://stale.local"
	if base, model := a.ResolveLLM(); base != "https://api.groq.com/openai/v1" || model != "llama-3.3-70b-versatile" {
		t.Fatalf("groq preset resolved to %q, %q", base, model)
	}

	a.LLMProvider = LLMProviderPollinations
	a.LLMModel = "custom-model"
	if base, model := a.ResolveLLM(); base != "https://text.pollinations.ai" || model != "custom-model" {
		t.Fatalf("pollinations resolved to %q, %q", base, model)
	}

	a.LLMProvider = LLMProviderCustom
	a.LLMBaseURL = "http://192.168.1.2:8080"
	if base, model := a.ResolveLLM(); base != "http://192.168.1.2:8080" || model != "custom-model" {
		t.Fatalf("custom resolved to %q, %q", base, model)
	}
}

func TestValidationRejectsUnknownLLMProvider(t *testing.T) {
	c := Default()
	c.Station.Callsign = "N0CALL"
	c.Station.DMRID = 2501234
	c.Agent.LLMProvider = "not-a-provider"
	errs := c.Validate()
	if len(errs) != 1 || !strings.Contains(errs[0], "llm_provider") {
		t.Fatalf("expected llm_provider error, got %v", errs)
	}
}

func TestWebSearchConfigValidation(t *testing.T) {
	c := Default()
	c.Mode = "agent"
	c.Station.Callsign = "N0CALL"
	c.Agent.WebSearch = true
	c.Agent.WebSearchProvider = "searxng"
	if errs := c.Validate(); len(errs) != 1 || !strings.Contains(errs[0], "web_search_base_url") {
		t.Fatalf("expected searxng URL error, got %v", errs)
	}

	c.Agent.WebSearchProvider = "duckduckgo"
	c.Agent.WebSearchMaxResults = 50
	c.Agent.WebSearchFetchPages = 9
	c.Agent.WebSearchTimeout = 0
	if errs := c.Validate(); len(errs) != 3 {
		t.Fatalf("expected three range errors, got %v", errs)
	}

	c.Agent.WebSearchMaxResults = 5
	c.Agent.WebSearchFetchPages = 2
	c.Agent.WebSearchTimeout = 12
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("valid web search config rejected: %v", errs)
	}
}

func TestOnlineProviderRequiresAPIKey(t *testing.T) {
	c := Default()
	c.Mode = "agent"
	c.Station.Callsign = "N0CALL"
	c.Agent.LLMProvider = LLMProviderOpenRouter
	errs := c.Validate()
	if len(errs) != 1 || !strings.Contains(errs[0], "llm_api_key") {
		t.Fatalf("expected missing API key error, got %v", errs)
	}
	c.Agent.LLMAPIKey = "test-key"
	if errs := c.Validate(); len(errs) != 0 {
		t.Fatalf("valid provider config rejected: %v", errs)
	}
}
