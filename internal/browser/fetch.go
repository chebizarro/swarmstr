// Package browser provides a lightweight HTTP-based browser skill.
//
// It fetches a URL via net/http and converts the response body to plain text.
// For HTML responses the tags are stripped and whitespace is normalised so the
// agent receives readable text rather than raw markup.  For JSON, plain text,
// and other non-HTML types the body is returned as-is (up to MaxBodyBytes).
package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	// MaxBodyBytes is the maximum response body read before truncation.
	MaxBodyBytes = 256 * 1024

	// DefaultTimeoutMS is the default request timeout in milliseconds.
	DefaultTimeoutMS = 30_000
)

// Request carries the parameters for a browser.request call.
type Request struct {
	Method    string
	URL       string
	Query     map[string]any
	Headers   map[string]string
	Body      any
	TimeoutMS int
}

// Response is the structured result of a browser.request call.
type Response struct {
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
	// Body is the raw response body (for non-HTML types).
	Body string `json:"body,omitempty"`
	// Text is the plain-text extraction (for HTML types).
	Text string `json:"text,omitempty"`
}

// Fetch performs an HTTP request and returns a structured Response.
func Fetch(ctx context.Context, req Request) (Response, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		return Response{}, fmt.Errorf("url is required")
	}

	// Append query parameters.
	if len(req.Query) > 0 {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return Response{}, fmt.Errorf("invalid url %q: %w", rawURL, err)
		}
		q := parsed.Query()
		for k, v := range req.Query {
			q.Set(k, fmt.Sprintf("%v", v))
		}
		parsed.RawQuery = q.Encode()
		rawURL = parsed.String()
	}

	// Build request body.
	var bodyReader io.Reader
	if req.Body != nil {
		switch v := req.Body.(type) {
		case string:
			bodyReader = strings.NewReader(v)
		case []byte:
			bodyReader = bytes.NewReader(v)
		default:
			encoded, err := json.Marshal(v)
			if err != nil {
				return Response{}, fmt.Errorf("marshal body: %w", err)
			}
			bodyReader = bytes.NewReader(encoded)
		}
	}

	timeoutMS := req.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = DefaultTimeoutMS
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return Response{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("User-Agent", "metiqd/browser-skill")
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	client := &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return Response{}, fmt.Errorf("read response body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")
	result := Response{
		StatusCode:  resp.StatusCode,
		ContentType: contentType,
		URL:         resp.Request.URL.String(),
	}

	bodyStr := string(bodyBytes)
	if isHTML(contentType) {
		result.Text = HTMLToText(bodyStr)
	} else {
		result.Body = bodyStr
	}
	return result, nil
}

// isHTML reports whether a Content-Type header indicates HTML content.
func isHTML(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "text/html") || strings.Contains(ct, "application/xhtml")
}

// HTMLToText strips HTML tags and normalises whitespace to produce plain text
// suitable for an LLM agent.  It handles common entities and collapses runs of
// whitespace into single spaces.
func HTMLToText(html string) string {
	// Remove <script> and <style> blocks including their contents.
	html = removeTagWithContent(html, "script")
	html = removeTagWithContent(html, "style")

	// Replace block-level / semantic elements with newlines.
	blockTags := []string{
		"p", "div", "br", "li", "tr", "td", "th",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"header", "footer", "nav", "section", "article", "aside", "main",
		"blockquote", "pre", "hr",
	}
	for _, tag := range blockTags {
		html = strings.NewReplacer(
			"<"+tag+">", "\n",
			"</"+tag+">", "\n",
			"<"+tag+" ", "\n<", // opening tag with attrs — keep a stub we'll strip
		).Replace(html)
		// Also handle uppercase variants.
		upper := strings.ToUpper(tag)
		html = strings.NewReplacer(
			"<"+upper+">", "\n",
			"</"+upper+">", "\n",
			"<"+upper+" ", "\n<",
		).Replace(html)
	}

	// Strip remaining tags.
	html = stripTags(html)

	// Decode common HTML entities.
	html = decodeEntities(html)

	// Normalise whitespace: collapse internal runs, trim lines.
	var sb strings.Builder
	for _, line := range strings.Split(html, "\n") {
		line = collapseWhitespace(line)
		if line != "" {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimSpace(sb.String())
}

// removeTagWithContent removes a tag and everything between its opening and
// closing tags (e.g. entire <script>…</script> blocks).
func removeTagWithContent(html, tag string) string {
	lower := strings.ToLower(html)
	open := "<" + tag
	close := "</" + tag + ">"
	for {
		start := strings.Index(lower, open)
		if start < 0 {
			break
		}
		end := strings.Index(lower[start:], close)
		if end < 0 {
			// No closing tag; remove from start to end of string.
			html = html[:start]
			lower = lower[:start]
			break
		}
		end += start + len(close)
		html = html[:start] + html[end:]
		lower = lower[:start] + lower[end:]
	}
	return html
}

// stripTags removes all remaining < … > sequences.
func stripTags(s string) string {
	var sb strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// decodeEntities replaces a small set of common HTML entities.
func decodeEntities(s string) string {
	r := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&nbsp;", " ",
		"&mdash;", "—",
		"&ndash;", "–",
		"&hellip;", "…",
		"&copy;", "©",
		"&reg;", "®",
	)
	return r.Replace(s)
}

// collapseWhitespace replaces runs of whitespace with a single space and trims.
func collapseWhitespace(s string) string {
	var sb strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				sb.WriteByte(' ')
			}
			prevSpace = true
		} else {
			sb.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(sb.String())
}
