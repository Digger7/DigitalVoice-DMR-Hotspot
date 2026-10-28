package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLMStudioOmitsEmptyModel(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	l := NewLMStudio(srv.URL, "", "", "", 0, 16, 5*time.Second)
	if got := l.Chat("привет"); got != "ok" {
		t.Fatalf("Chat returned %q", got)
	}
	if raw, ok := body["model"]; ok {
		t.Fatalf("empty model must be omitted, got %s", raw)
	}
}

func TestLMStudioSendsConfiguredModel(t *testing.T) {
	var body map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	l := NewLMStudio(srv.URL, "local-model", "", "", 0, 16, 5*time.Second)
	if got := l.Chat("привет"); got != "ok" {
		t.Fatalf("Chat returned %q", got)
	}
	var model string
	if err := json.Unmarshal(body["model"], &model); err != nil {
		t.Fatalf("model field: %v", err)
	}
	if model != "local-model" {
		t.Fatalf("model = %q", model)
	}
}

func TestLMStudioEndpointBuildsOpenAIPath(t *testing.T) {
	cases := map[string]string{
		"http://localhost:1234":                                   "http://localhost:1234/v1/chat/completions",
		"http://localhost:1234/":                                  "http://localhost:1234/v1/chat/completions",
		"https://api.groq.com/openai/v1":                          "https://api.groq.com/openai/v1/chat/completions",
		"https://generativelanguage.googleapis.com/v1beta/openai": "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		"https://text.pollinations.ai":                            "https://text.pollinations.ai/v1/chat/completions",
	}
	for base, want := range cases {
		l := NewLMStudio(base, "", "", "", 0, 16, time.Second)
		if got := l.endpoint("/chat/completions"); got != want {
			t.Errorf("endpoint(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestLMStudioTestConnectionAcceptsPlainTextModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "model list is served as plain text here")
	}))
	defer srv.Close()

	l := NewLMStudio(srv.URL, "", "", "", 0, 16, time.Second)
	ok, detail := l.TestConnection(time.Second)
	if !ok {
		t.Fatalf("TestConnection failed: %s", detail)
	}
	if !strings.Contains(detail, "connected") {
		t.Fatalf("unexpected detail: %q", detail)
	}
}
