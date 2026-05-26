package linkunderstanding

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

var urlRE = regexp.MustCompile(`(?i)\bhttps?://[^\s<>()\[\]{}"']+`)

// Link is a URL found in a message.
type Link struct {
	URL  string `json:"url"`
	Text string `json:"text,omitempty"`
}

// Metadata is fetched page metadata suitable for prompt/context assembly.
type Metadata struct {
	URL         string `json:"url"`
	FinalURL    string `json:"final_url,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// FetchOptions controls guarded metadata fetching.
type FetchOptions struct {
	Client       *http.Client
	MaxBytes     int64
	Timeout      time.Duration
	AllowPrivate bool
}

// ExtractURLs extracts http(s) URLs from free-form message text. It preserves
// first-seen order and removes trailing sentence punctuation.
func ExtractURLs(message string) []Link {
	seen := map[string]bool{}
	var out []Link
	for _, raw := range urlRE.FindAllString(message, -1) {
		u := strings.TrimRight(raw, ".,;:!?)]}\"'")
		parsed, err := url.Parse(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			continue
		}
		key := parsed.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Link{URL: key, Text: raw})
	}
	return out
}

// FetchMetadata fetches a URL with SSRF guards and extracts title, description,
// Open Graph metadata, and a compact text summary.
func FetchMetadata(ctx context.Context, rawURL string, opts FetchOptions) (Metadata, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return Metadata{}, fmt.Errorf("invalid URL: %q", rawURL)
	}
	if !opts.AllowPrivate {
		if err := rejectPrivateHost(ctx, parsed.Hostname()); err != nil {
			return Metadata{}, err
		}
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: firstDuration(opts.Timeout, 10*time.Second)}
	}
	ctx, cancel := context.WithTimeout(ctx, firstDuration(opts.Timeout, 10*time.Second))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Metadata{}, err
	}
	req.Header.Set("User-Agent", "metiq-link-understanding/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	resp, err := client.Do(req)
	if err != nil {
		return Metadata{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Metadata{}, fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}
	limit := opts.MaxBytes
	if limit <= 0 {
		limit = 512 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return Metadata{}, err
	}
	meta := ParseMetadata(string(body))
	meta.URL = parsed.String()
	meta.FinalURL = resp.Request.URL.String()
	meta.ContentType = resp.Header.Get("Content-Type")
	if meta.Summary == "" {
		meta.Summary = SummarizeContent(string(body), 360)
	}
	return meta, nil
}

// ParseMetadata extracts title/description/Open Graph metadata from HTML.
func ParseMetadata(doc string) Metadata {
	props := map[string]string{}
	for _, m := range metaTagRE.FindAllStringSubmatch(doc, -1) {
		attrs := parseAttrs(m[1])
		key := strings.ToLower(firstNonEmpty(attrs["property"], attrs["name"]))
		content := strings.TrimSpace(html.UnescapeString(attrs["content"]))
		if key != "" && content != "" {
			props[key] = content
		}
	}
	m := Metadata{
		Title:       firstNonEmpty(props["og:title"], titleFromHTML(doc)),
		Description: firstNonEmpty(props["og:description"], props["description"]),
		SiteName:    props["og:site_name"],
	}
	m.Summary = SummarizeContent(doc, 360)
	return m
}

var (
	metaTagRE = regexp.MustCompile(`(?is)<meta\s+([^>]+)>`)
	attrRE    = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	titleRE   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	tagRE     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<[^>]+>`)
	spaceRE   = regexp.MustCompile(`\s+`)
)

func parseAttrs(src string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRE.FindAllStringSubmatch(src, -1) {
		v := strings.Trim(m[2], `"'`)
		out[strings.ToLower(m[1])] = v
	}
	return out
}

func titleFromHTML(doc string) string {
	m := titleRE.FindStringSubmatch(doc)
	if len(m) < 2 {
		return ""
	}
	return cleanText(m[1])
}

// SummarizeContent strips markup and returns a compact, deterministic summary.
func SummarizeContent(content string, maxChars int) string {
	text := cleanText(content)
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	cut := maxChars
	if idx := strings.LastIndexAny(text[:maxChars], ".!?\n"); idx > maxChars/2 {
		cut = idx + 1
	} else if idx := strings.LastIndex(text[:maxChars], " "); idx > maxChars/2 {
		cut = idx
	}
	return strings.TrimSpace(text[:cut]) + "…"
}

// AssembleContext renders fetched link metadata for inclusion in an agent prompt.
func AssembleContext(items []Metadata) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		urlText := firstNonEmpty(item.FinalURL, item.URL)
		bits := []string{"- " + firstNonEmpty(item.Title, urlText)}
		if item.Description != "" {
			bits = append(bits, item.Description)
		}
		if item.Summary != "" && item.Summary != item.Description {
			bits = append(bits, item.Summary)
		}
		bits = append(bits, "URL: "+urlText)
		parts = append(parts, strings.Join(bits, "\n  "))
	}
	sort.Strings(parts)
	return "Linked resources:\n" + strings.Join(parts, "\n")
}

func cleanText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func rejectPrivateHost(ctx context.Context, host string) error {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return errors.New("host resolved to no addresses")
	}
	for _, addr := range ips {
		ip := addr.IP
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("blocked private/link-local host %s", host)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstDuration(values ...time.Duration) time.Duration {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}
