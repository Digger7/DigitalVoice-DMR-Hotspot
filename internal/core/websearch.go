package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// SearchResult is one hit returned by a web search provider.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchOptions configures the search/fetch helper used by the LLM tools.
type WebSearchOptions struct {
	Provider   string
	BaseURL    string
	Language   string
	MaxResults int
	FetchPages int
	Timeout    time.Duration
}

// WebSearch performs keyless web searches and extracts readable text from the
// result pages. It is safe for concurrent use.
type WebSearch struct {
	provider   string
	baseURL    string
	language   string
	maxResults int
	fetchPages int
	timeout    time.Duration
	ddgURL     string
	allowLocal bool
	client     *http.Client
}

const (
	webSearchDuckDuckGo = "duckduckgo"
	webSearchSearXNG    = "searxng"

	webSearchUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36"

	maxPageBytes   = 512 * 1024
	maxPageRunes   = 3000
	maxSearchRunes = 12000
)

var (
	reAnchor     = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	reSnippet    = regexp.MustCompile(`(?is)<td\b([^>]*)>(.*?)</td>`)
	reHref       = regexp.MustCompile(`(?is)\bhref\s*=\s*["']([^"']*)["']`)
	reClass      = regexp.MustCompile(`(?is)\bclass\s*=\s*["']([^"']*)["']`)
	reComment    = regexp.MustCompile(`(?s)<!--.*?-->`)
	reAnyTag     = regexp.MustCompile(`(?s)<[^>]*>`)
	reBreak      = regexp.MustCompile(`(?is)<(?:br|hr)\s*/?>`)
	reBlockEnd   = regexp.MustCompile(`(?is)</(?:p|div|li|tr|h[1-6]|section|article|header|footer|table|ul|ol)\s*>`)
	reWhitespace = regexp.MustCompile(`[\s\x{00a0}]+`)
)

func NewWebSearch(o WebSearchOptions) *WebSearch {
	provider := strings.ToLower(strings.TrimSpace(o.Provider))
	switch provider {
	case "", "ddg", "duck-duck-go":
		provider = webSearchDuckDuckGo
	case "searx", "searxng":
		provider = webSearchSearXNG
	}
	maxResults := o.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	if maxResults > 10 {
		maxResults = 10
	}
	fetchPages := o.FetchPages
	if fetchPages < 0 {
		fetchPages = 0
	}
	if fetchPages > 5 {
		fetchPages = 5
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	w := &WebSearch{
		provider: provider, baseURL: strings.TrimRight(strings.TrimSpace(o.BaseURL), "/"),
		language: strings.TrimSpace(o.Language), maxResults: maxResults,
		fetchPages: fetchPages, timeout: timeout, ddgURL: "https://lite.duckduckgo.com/lite/",
	}
	w.client = &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if w.allowLocal {
				return nil
			}
			return validatePublicHost(req.Context(), req.URL.Hostname())
		},
	}
	return w
}

// Timeout returns the per-request HTTP timeout.
func (w *WebSearch) Timeout() time.Duration { return w.timeout }

// Budget returns a sensible overall deadline for one model tool call.
func (w *WebSearch) Budget() time.Duration {
	budget := w.timeout * 3
	if budget < 20*time.Second {
		budget = 20 * time.Second
	}
	return budget
}

// Search returns the provider hits for query.
func (w *WebSearch) Search(ctx context.Context, query string) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("empty search query")
	}
	switch w.provider {
	case webSearchSearXNG:
		return w.searchSearXNG(ctx, query)
	default:
		return w.searchDuckDuckGo(ctx, query)
	}
}

// SearchText runs a search and returns a compact text block for the model,
// including excerpts from the top result pages when configured.
func (w *WebSearch) SearchText(ctx context.Context, query string) string {
	results, err := w.Search(ctx, query)
	if err != nil {
		return "Web search failed: " + err.Error()
	}
	if len(results) == 0 {
		return "Web search returned no results."
	}
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s\nURL: %s\n%s\n", i+1, r.Title, r.URL, r.Snippet)
	}
	if w.fetchPages == 0 {
		return b.String()
	}

	n := w.fetchPages
	if n > len(results) {
		n = len(results)
	}
	type page struct {
		index int
		text  string
	}
	pages := make([]page, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			text, err := w.Fetch(ctx, results[i].URL)
			if err != nil {
				return
			}
			pages[i] = page{index: i, text: text}
		}(i)
	}
	wg.Wait()
	for _, p := range pages {
		if p.text == "" {
			continue
		}
		fmt.Fprintf(&b, "\n--- Page %d: %s ---\n%s\n", p.index+1, results[p.index].URL, p.text)
		if b.Len() > maxSearchRunes {
			break
		}
	}
	return truncateRunes(b.String(), maxSearchRunes)
}

func (w *WebSearch) searchDuckDuckGo(ctx context.Context, query string) ([]SearchResult, error) {
	values := url.Values{}
	values.Set("q", query)
	if region := ddgRegion(w.language); region != "" {
		values.Set("kl", region)
	}
	endpoint := w.ddgURL
	if endpoint == "" {
		endpoint = "https://lite.duckduckgo.com/lite/"
	}
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	body, err := w.get(ctx, endpoint+sep+values.Encode(), "text/html,application/xhtml+xml")
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoLite(string(body), w.maxResults), nil
}

func (w *WebSearch) searchSearXNG(ctx context.Context, query string) ([]SearchResult, error) {
	if w.baseURL == "" {
		return nil, errors.New("searxng base URL is empty")
	}
	values := url.Values{}
	values.Set("q", query)
	values.Set("format", "json")
	if w.language != "" {
		values.Set("language", w.language)
	}
	body, err := w.get(ctx, w.baseURL+"/search?"+values.Encode(), "application/json")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("searxng response: %w", err)
	}
	out := make([]SearchResult, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		if strings.TrimSpace(r.URL) == "" {
			continue
		}
		out = append(out, SearchResult{
			Title: cleanText(r.Title), URL: strings.TrimSpace(r.URL), Snippet: cleanText(r.Content),
		})
		if len(out) >= w.maxResults {
			break
		}
	}
	return out, nil
}

// Fetch downloads one public http(s) URL and returns plain text.
func (w *WebSearch) Fetch(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("only http/https URLs are allowed")
	}
	if u.Hostname() == "" {
		return "", errors.New("URL has no host")
	}
	if !w.allowLocal {
		if err := validatePublicHost(ctx, u.Hostname()); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", webSearchUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")
	req.Header.Set("Accept-Language", acceptLanguage(w.language))
	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType != "" &&
		!strings.Contains(contentType, "text/html") &&
		!strings.Contains(contentType, "text/plain") &&
		!strings.Contains(contentType, "application/xhtml") &&
		!strings.Contains(contentType, "application/json") {
		return "", fmt.Errorf("unsupported content type %q", contentType)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return "", err
	}
	text := string(data)
	if strings.Contains(contentType, "text/html") || looksLikeHTML(text) {
		text = htmlToText(text)
	} else {
		text = cleanText(text)
	}
	return truncateRunes(text, maxPageRunes), nil
}

func (w *WebSearch) get(ctx context.Context, rawURL, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", webSearchUserAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", acceptLanguage(w.language))
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2*maxPageBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateRunes(cleanText(string(data)), 200))
	}
	return data, nil
}

func parseDuckDuckGoLite(page string, max int) []SearchResult {
	var results []SearchResult
	for _, m := range reAnchor.FindAllStringSubmatch(page, -1) {
		attrs, inner := m[1], m[2]
		if !strings.Contains(strings.ToLower(attrs), "result-link") {
			continue
		}
		href := ""
		if h := reHref.FindStringSubmatch(attrs); h != nil {
			href = ddgRealURL(h[1])
		}
		if href == "" {
			continue
		}
		results = append(results, SearchResult{Title: cleanText(inner), URL: href})
		if max > 0 && len(results) >= max {
			break
		}
	}
	var snippets []string
	for _, m := range reSnippet.FindAllStringSubmatch(page, -1) {
		if !strings.Contains(strings.ToLower(m[1]), "result-snippet") {
			continue
		}
		snippets = append(snippets, cleanText(m[2]))
	}
	for i := range results {
		if i < len(snippets) {
			results[i].Snippet = snippets[i]
		}
	}
	return results
}

func ddgRealURL(raw string) string {
	raw = html.UnescapeString(strings.TrimSpace(raw))
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if target := u.Query().Get("uddg"); target != "" {
		return target
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func htmlToText(page string) string {
	page = reComment.ReplaceAllString(page, " ")
	page = stripElements(page, "script")
	page = stripElements(page, "style")
	page = stripElements(page, "noscript")
	page = stripElements(page, "svg")
	page = stripElements(page, "head")
	page = stripElements(page, "template")
	page = reBreak.ReplaceAllString(page, "\n")
	page = reBlockEnd.ReplaceAllString(page, "\n")
	page = reAnyTag.ReplaceAllString(page, " ")
	return cleanText(page)
}

func stripElements(page, tag string) string {
	re := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(tag) + `\b[^>]*>.*?</` + regexp.QuoteMeta(tag) + `\s*>`)
	return re.ReplaceAllString(page, " ")
}

func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = reAnyTag.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(reWhitespace.ReplaceAllString(s, " "))
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func looksLikeHTML(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<body") || strings.Contains(lower, "<!doctype")
}

func acceptLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return "en-US,en;q=0.8"
	}
	return language + "," + language + "-" + strings.ToUpper(language) + ";q=0.9,en;q=0.7"
}

func ddgRegion(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if len(language) < 2 {
		return ""
	}
	lang := language[:2]
	switch lang {
	case "ru":
		return "ru-ru"
	case "uk":
		return "ua-uk"
	case "en":
		return "us-en"
	case "de":
		return "de-de"
	case "fr":
		return "fr-fr"
	case "es":
		return "es-es"
	case "it":
		return "it-it"
	case "pt":
		return "pt-pt"
	case "pl":
		return "pl-pl"
	case "tr":
		return "tr-tr"
	default:
		return lang + "-" + lang
	}
}

var blockedIPNets = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24",
		"198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		if _, ipnet, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, ipnet)
		}
	}
	return nets
}()

func validatePublicHost(ctx context.Context, host string) error {
	if host == "" {
		return errors.New("empty host")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, addr := range ips {
		if isBlockedIP(addr.IP) {
			return fmt.Errorf("refusing to access private or local address %s", addr.IP)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, ipnet := range blockedIPNets {
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}
