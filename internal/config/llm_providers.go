package config

import "strings"

// LLMProvider describes a hosted or local OpenAI-compatible chat endpoint.
type LLMProvider struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	NameRU       string   `json:"name_ru,omitempty"`
	BaseURL      string   `json:"base_url"`
	DefaultModel string   `json:"default_model,omitempty"`
	Models       []string `json:"models,omitempty"`
	RequiresKey  bool     `json:"requires_key"`
	Note         string   `json:"note,omitempty"`
	NoteRU       string   `json:"note_ru,omitempty"`
}

const (
	LLMProviderCustom       = "custom"
	LLMProviderPollinations = "pollinations"
	LLMProviderOpenRouter   = "openrouter"
	LLMProviderGroq         = "groq"
	LLMProviderGemini       = "gemini"
	LLMProviderMistral      = "mistral"
	LLMProviderCerebras     = "cerebras"
	LLMProviderHuggingFace  = "huggingface"
)

var llmProviders = []LLMProvider{
	{
		ID:     LLMProviderCustom,
		Name:   "Local / custom",
		NameRU: "Локальная / своя",
		Note:   "Any OpenAI-compatible endpoint (LM Studio, Ollama, llama.cpp).",
		NoteRU: "Любой OpenAI-совместимый сервер (LM Studio, Ollama, llama.cpp).",
	},
	{
		ID:           LLMProviderPollinations,
		Name:         "Pollinations (free, no key)",
		NameRU:       "Pollinations (бесплатно, без ключа)",
		BaseURL:      "https://text.pollinations.ai",
		DefaultModel: "openai",
		Models:       []string{"openai", "openai-large", "mistral"},
		Note:         "Free hosted models, no registration. One request per 30 seconds per IP.",
		NoteRU:       "Бесплатные модели без регистрации. Не чаще одного запроса в 30 секунд с IP.",
	},
	{
		ID:           LLMProviderOpenRouter,
		Name:         "OpenRouter (free models)",
		NameRU:       "OpenRouter (бесплатные модели)",
		BaseURL:      "https://openrouter.ai/api/v1",
		DefaultModel: "meta-llama/llama-3.3-70b-instruct:free",
		Models:       []string{"meta-llama/llama-3.3-70b-instruct:free", "deepseek/deepseek-chat-v3-0324:free", "google/gemini-2.0-flash-exp:free"},
		RequiresKey:  true,
		Note:         "Free models end with :free. Get a key at openrouter.ai/keys.",
		NoteRU:       "Бесплатные модели имеют суффикс :free. Ключ — на openrouter.ai/keys.",
	},
	{
		ID:           LLMProviderGroq,
		Name:         "Groq (free tier)",
		NameRU:       "Groq (бесплатный тариф)",
		BaseURL:      "https://api.groq.com/openai/v1",
		DefaultModel: "llama-3.3-70b-versatile",
		Models:       []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant", "openai/gpt-oss-120b"},
		RequiresKey:  true,
		Note:         "Generous free tier, very fast. Get a key at console.groq.com/keys.",
		NoteRU:       "Щедрый бесплатный тариф, очень быстро. Ключ — на console.groq.com/keys.",
	},
	{
		ID:           LLMProviderGemini,
		Name:         "Google Gemini (free tier)",
		NameRU:       "Google Gemini (бесплатный тариф)",
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta/openai",
		DefaultModel: "gemini-2.0-flash",
		Models:       []string{"gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-flash-lite"},
		RequiresKey:  true,
		Note:         "Free tier via Google AI Studio. Get a key at aistudio.google.com/apikey.",
		NoteRU:       "Бесплатный тариф Google AI Studio. Ключ — на aistudio.google.com/apikey.",
	},
	{
		ID:           LLMProviderMistral,
		Name:         "Mistral (free tier)",
		NameRU:       "Mistral (бесплатный тариф)",
		BaseURL:      "https://api.mistral.ai/v1",
		DefaultModel: "mistral-small-latest",
		Models:       []string{"mistral-small-latest", "open-mistral-nemo", "mistral-large-latest"},
		RequiresKey:  true,
		Note:         "Free experiment tier. Get a key at console.mistral.ai/api-keys.",
		NoteRU:       "Бесплатный экспериментальный тариф. Ключ — на console.mistral.ai/api-keys.",
	},
	{
		ID:           LLMProviderCerebras,
		Name:         "Cerebras (free tier)",
		NameRU:       "Cerebras (бесплатный тариф)",
		BaseURL:      "https://api.cerebras.ai/v1",
		DefaultModel: "llama-3.3-70b",
		Models:       []string{"llama-3.3-70b", "llama3.1-8b"},
		RequiresKey:  true,
		Note:         "Free tier with very high speed. Get a key at cloud.cerebras.ai.",
		NoteRU:       "Бесплатный тариф с очень высокой скоростью. Ключ — на cloud.cerebras.ai.",
	},
	{
		ID:           LLMProviderHuggingFace,
		Name:         "Hugging Face (free tier)",
		NameRU:       "Hugging Face (бесплатный тариф)",
		BaseURL:      "https://router.huggingface.co/v1",
		DefaultModel: "meta-llama/Llama-3.3-70B-Instruct",
		Models:       []string{"meta-llama/Llama-3.3-70B-Instruct", "Qwen/Qwen2.5-72B-Instruct"},
		RequiresKey:  true,
		Note:         "Inference Providers router. Use a token with the Inference permission.",
		NoteRU:       "Роутер Inference Providers. Нужен токен с разрешением Inference.",
	},
}

// LLMProviders returns a copy of the built-in provider presets.
func LLMProviders() []LLMProvider {
	out := make([]LLMProvider, len(llmProviders))
	copy(out, llmProviders)
	for i := range out {
		out[i].Models = append([]string(nil), out[i].Models...)
	}
	return out
}

// LookupLLMProvider finds a preset by its stable identifier.
func LookupLLMProvider(id string) (LLMProvider, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, p := range llmProviders {
		if p.ID == id {
			return p, true
		}
	}
	return LLMProvider{}, false
}

// ResolveLLM returns the effective endpoint and model. Named providers use
// their built-in base URL; "custom" (or an empty provider) uses the configured
// URL so local servers and self-hosted proxies keep working.
func (a AgentConfig) ResolveLLM() (baseURL, model string) {
	baseURL = strings.TrimSpace(a.LLMBaseURL)
	model = strings.TrimSpace(a.LLMModel)
	if p, ok := LookupLLMProvider(a.LLMProvider); ok && p.ID != LLMProviderCustom && p.BaseURL != "" {
		baseURL = p.BaseURL
		if model == "" {
			model = p.DefaultModel
		}
	}
	return baseURL, model
}
