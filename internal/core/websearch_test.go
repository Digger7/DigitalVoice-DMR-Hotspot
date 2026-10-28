package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseDuckDuckGoLite(t *testing.T) {
	page := `<html><body>
<a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage%3Fa%3D1&amp;rut=abc" class='result-link'>Example &amp; Co</a>
<td class='result-snippet'>Hello &lt;b&gt;world&lt;/b&gt;</td>
<a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fsecond.example%2F" class='result-link'>Second</a>
<td class='result-snippet'>Another result</td>
</body></html>`
	got := parseDuckDuckGoLite(page, 5)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	if got[0].Title != "Example & Co" || got[0].URL != "https://example.com/page?a=1" {
		t.Fatalf("first result = %+v", got[0])
	}
	if got[0].Snippet != "Hello world" {
		t.Fatalf("snippet = %q", got[0].Snippet)
	}
	if got[1].URL != "https://second.example/" {
		t.Fatalf("second URL = %q", got[1].URL)
	}
}

func TestWebSearchSearXNG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("format") != "json" || r.URL.Query().Get("q") != "test" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"results":[{"title":"One","url":"https://one.example","content":"First"},{"title":"Two","url":"https://two.example","content":"Second"}]}`)
	}))
	defer srv.Close()

	w := NewWebSearch(WebSearchOptions{
		Provider: "searxng", BaseURL: srv.URL, MaxResults: 1,
		FetchPages: 0, Timeout: 2 * time.Second,
	})
	results, err := w.Search(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "One" || results[0].URL != "https://one.example" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWebFetchBlocksLocalAddresses(t *testing.T) {
	w := NewWebSearch(WebSearchOptions{Timeout: time.Second})
	if _, err := w.Fetch(context.Background(), "http://127.0.0.1:1/"); err == nil {
		t.Fatal("expected local address to be rejected")
	}
	if _, err := w.Fetch(context.Background(), "file:///etc/passwd"); err == nil {
		t.Fatal("expected non-http scheme to be rejected")
	}
}

func TestWebFetchExtractsReadableText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<html><head><title>x</title><script>secret()</script></head>
<body><h1>Заголовок</h1><p>Hello&nbsp;world &amp; friends</p><style>.x{}</style></body></html>`)
	}))
	defer srv.Close()

	w := NewWebSearch(WebSearchOptions{Timeout: 2 * time.Second})
	w.allowLocal = true
	text, err := w.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Заголовок") || !strings.Contains(text, "Hello world & friends") {
		t.Fatalf("unexpected text: %q", text)
	}
	if strings.Contains(text, "secret") || strings.Contains(text, ".x{}") {
		t.Fatalf("script/style leaked into text: %q", text)
	}
}

func TestLMStudioWebSearchToolLoop(t *testing.T) {
	var llmCalls int
	var toolResult string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			_, _ = io.WriteString(w, `{"results":[{"title":"Погода","url":"https://weather.example","content":"В Москве плюс пять"}]}`)
		case "/v1/chat/completions":
			llmCalls++
			var body chatRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
				return
			}
			if llmCalls == 1 {
				if len(body.Tools) == 0 {
					t.Error("tools were not sent")
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"погода москва\"}"}}]},"finish_reason":"tool_calls"}]}`)
				return
			}
			for _, m := range body.Messages {
				if m.Role == "tool" {
					toolResult = m.Content
				}
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"В Москве плюс пять."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	l := NewLMStudio(srv.URL, "model", "system", "", 0, 128, 5*time.Second)
	l.Search = NewWebSearch(WebSearchOptions{
		Provider: "searxng", BaseURL: srv.URL, MaxResults: 3,
		FetchPages: 0, Timeout: 2 * time.Second,
	})
	if got := l.Chat("Какая погода в Москве?"); got != "В Москве плюс пять." {
		t.Fatalf("Chat = %q", got)
	}
	if llmCalls < 2 {
		t.Fatalf("expected a second LLM call, got %d", llmCalls)
	}
	if !strings.Contains(toolResult, "В Москве плюс пять") {
		t.Fatalf("tool result not passed back: %q", toolResult)
	}
}

func TestLMStudioFallsBackToDirectSearch(t *testing.T) {
	var llmCalls, searchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			searchCalls++
			_, _ = io.WriteString(w, `{"results":[{"title":"Факт","url":"https://fact.example","content":"Вода мокрая"}]}`)
		case "/v1/chat/completions":
			llmCalls++
			var body chatRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
				return
			}
			if len(body.Tools) > 0 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"tools are not supported"}`)
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Вода мокрая."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	l := NewLMStudio(srv.URL, "model", "system", "", 0, 128, 5*time.Second)
	l.Search = NewWebSearch(WebSearchOptions{
		Provider: "searxng", BaseURL: srv.URL, MaxResults: 3,
		FetchPages: 0, Timeout: 2 * time.Second,
	})
	if got := l.Chat("Вода мокрая?"); got != "Вода мокрая." {
		t.Fatalf("Chat = %q", got)
	}
	if llmCalls < 2 || searchCalls == 0 {
		t.Fatalf("fallback not used: llm=%d search=%d", llmCalls, searchCalls)
	}
}

func TestLMStudioAccessDeniedSkipsSearch(t *testing.T) {
	var searchCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search" {
			searchCalls++
			_, _ = io.WriteString(w, `{"results":[]}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"error":"Access denied by security policy."}`)
	}))
	defer srv.Close()

	l := NewLMStudio(srv.URL, "model", "system", "", 0, 128, 5*time.Second)
	l.Search = NewWebSearch(WebSearchOptions{
		Provider: "searxng", BaseURL: srv.URL, MaxResults: 3,
		FetchPages: 0, Timeout: 2 * time.Second,
	})
	if got := l.Chat("test"); got != "" {
		t.Fatalf("Chat = %q, want empty", got)
	}
	if searchCalls != 0 {
		t.Fatalf("search was called %d times after an auth error", searchCalls)
	}
}
