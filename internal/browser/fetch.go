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
	"net"
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
	StatusCode  int         `json:"status_code"`
	ContentType string      `json:"content_type"`
	URL         string      `json:"url"`
	Headers     http.Header `json:"-"`
	// Body is the raw response body (for non-HTML types).
	Body string `json:"body,omitempty"`
	// Text is the plain-text extraction (for HTML types).
	Text string `json:"text,omitempty"`
}

// Client is a reusable browser transport. ValidateURL is applied to the
// initial request and to every redirect destination.
type Client struct {
	HTTPClient  *http.Client
	ValidateURL func(string) error
	// ValidateIP is applied to every resolved address immediately before dial.
	// When set, proxying and custom TLS dialers are disabled so the validated
	// address is the address actually contacted.
	ValidateIP func(net.IP) error
	// LookupIP is an optional deterministic resolver seam for tests.
	LookupIP func(context.Context, string) ([]net.IP, error)
}

// Fetch performs an HTTP request with the default browser client.
func Fetch(ctx context.Context, req Request) (Response, error) {
	return (&Client{}).Fetch(ctx, req)
}

// Fetch performs an HTTP request and returns a structured Response.
func (c *Client) Fetch(ctx context.Context, req Request) (Response, error) {
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

	if c != nil && c.ValidateURL != nil {
		if err := c.ValidateURL(rawURL); err != nil {
			return Response{}, fmt.Errorf("validate url: %w", err)
		}
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

	baseClient := http.DefaultClient
	if c != nil && c.HTTPClient != nil {
		baseClient = c.HTTPClient
	}
	client := *baseClient
	validatedTransport := false
	if c != nil && c.ValidateIP != nil {
		if err := c.installValidatedDialer(&client); err != nil {
			return Response{}, err
		}
		validatedTransport = true
	}
	if validatedTransport {
		defer client.CloseIdleConnections()
	}
	baseRedirect := baseClient.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if c != nil && c.ValidateURL != nil {
			if err := c.ValidateURL(next.URL.String()); err != nil {
				return fmt.Errorf("validate redirect url: %w", err)
			}
		}
		if baseRedirect != nil {
			if err := baseRedirect(next, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		// Strip credentials after the wrapped callback so it cannot reinsert
		// them. Exact-origin comparison is intentionally stricter than Go's
		// default host/domain-only sensitive-header policy.
		if len(via) > 0 && !sameOrigin(via[len(via)-1].URL, next.URL) {
			next.Header.Del("Authorization")
			next.Header.Del("Proxy-Authorization")
			next.Header.Del("Cookie")
		}
		return nil
	}
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
		Headers:     resp.Header.Clone(),
	}

	bodyStr := string(bodyBytes)
	if isHTML(contentType) {
		result.Text = HTMLToText(bodyStr)
	} else {
		result.Body = bodyStr
	}
	return result, nil
}

func (c *Client) installValidatedDialer(client *http.Client) error {
	var transport *http.Transport
	switch base := client.Transport.(type) {
	case nil:
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return fmt.Errorf("default HTTP transport does not support validated dialing")
		}
		transport = defaultTransport.Clone()
	case *http.Transport:
		transport = base.Clone()
	default:
		return fmt.Errorf("custom HTTP transport does not support validated dialing")
	}
	dial := transport.DialContext
	if dial == nil {
		dialer := &net.Dialer{}
		dial = dialer.DialContext
	}
	lookup := c.LookupIP
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		}
	}
	transport.Proxy = nil
	transport.DialTLS = nil
	transport.DialTLSContext = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address")
		}
		var addresses []net.IP
		if literal := net.ParseIP(host); literal != nil {
			addresses = []net.IP{literal}
		} else {
			addresses, err = lookup(ctx, host)
			if err != nil || len(addresses) == 0 {
				return nil, fmt.Errorf("resolve dial host: %w", err)
			}
		}
		for _, ip := range addresses {
			if err := c.ValidateIP(ip); err != nil {
				return nil, err
			}
		}
		var dialErr error
		for _, ip := range addresses {
			connection, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErr = err
		}
		return nil, dialErr
	}
	client.Transport = transport
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || !strings.EqualFold(a.Scheme, b.Scheme) || !strings.EqualFold(a.Hostname(), b.Hostname()) {
		return false
	}
	return effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
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
