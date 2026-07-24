package nip96

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	nostr "fiatjaf.com/nostr"
	"metiq/internal/nostr/nip98"
)

const KindServerPreference nostr.Kind = 10096

type Plan struct {
	Name                 string              `json:"name"`
	NIP98Required        *bool               `json:"is_nip98_required,omitempty"`
	URL                  string              `json:"url,omitempty"`
	MaxByteSize          int64               `json:"max_byte_size,omitempty"`
	FileExpiration       []int               `json:"file_expiration,omitempty"`
	MediaTransformations map[string][]string `json:"media_transformations,omitempty"`
}
type ServerConfig struct {
	APIURL         string          `json:"api_url"`
	DownloadURL    string          `json:"download_url,omitempty"`
	DelegatedToURL string          `json:"delegated_to_url,omitempty"`
	SupportedNIPs  []int           `json:"supported_nips,omitempty"`
	TOSURL         string          `json:"tos_url,omitempty"`
	ContentTypes   []string        `json:"content_types,omitempty"`
	Plans          map[string]Plan `json:"plans,omitempty"`
}
type UploadOptions struct {
	Filename    string
	Data        []byte
	Caption     string
	Alt         string
	Expiration  int64
	MediaType   string
	ContentType string
	NoTransform bool
	Fields      map[string]string
}
type APIResponse struct {
	Status        string       `json:"status"`
	Message       string       `json:"message"`
	ProcessingURL string       `json:"processing_url,omitempty"`
	NIP94Event    *nostr.Event `json:"nip94_event,omitempty"`
	Percentage    int          `json:"percentage,omitempty"`
}
type FileList struct {
	Count int           `json:"count"`
	Total int           `json:"total"`
	Page  int           `json:"page"`
	Files []nostr.Event `json:"files"`
}

type Client struct {
	Config ServerConfig
	HTTP   *http.Client
	Keyer  nostr.Signer
}

func Discover(ctx context.Context, origin string, client *http.Client) (ServerConfig, error) {
	return discover(ctx, origin, client, map[string]struct{}{}, 0)
}
func discover(ctx context.Context, origin string, client *http.Client, seen map[string]struct{}, depth int) (ServerConfig, error) {
	if depth > 3 {
		return ServerConfig{}, fmt.Errorf("NIP-96 delegation limit exceeded")
	}
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ServerConfig{}, fmt.Errorf("NIP-96 origin must be HTTPS")
	}
	origin = u.Scheme + "://" + u.Host
	if _, ok := seen[origin]; ok {
		return ServerConfig{}, fmt.Errorf("NIP-96 delegation loop")
	}
	seen[origin] = struct{}{}
	wellKnown := origin + "/.well-known/nostr/nip96.json"
	hc := noRedirectClient(client)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, wellKnown, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return ServerConfig{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ServerConfig{}, fmt.Errorf("NIP-96 discovery returned %s", resp.Status)
	}
	var cfg ServerConfig
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cfg) != nil {
		return ServerConfig{}, fmt.Errorf("invalid NIP-96 discovery document")
	}
	if cfg.DelegatedToURL != "" {
		if cfg.APIURL != "" || cfg.DownloadURL != "" || len(cfg.Plans) > 0 || len(cfg.ContentTypes) > 0 {
			return ServerConfig{}, fmt.Errorf("invalid delegated NIP-96 document")
		}
		return discover(ctx, cfg.DelegatedToURL, client, seen, depth+1)
	}
	if err := validateConfig(cfg); err != nil {
		return ServerConfig{}, err
	}
	return cfg, nil
}
func validateConfig(cfg ServerConfig) error {
	if cfg.APIURL == "" {
		return fmt.Errorf("NIP-96 api_url required")
	}
	for _, raw := range []string{cfg.APIURL, cfg.DownloadURL, cfg.TOSURL} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("NIP-96 URLs must use HTTPS")
		}
	}
	return nil
}
func noRedirectClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &clone
}

func (c *Client) Upload(ctx context.Context, opts UploadOptions) (APIResponse, error) {
	if err := validateConfig(c.Config); err != nil {
		return APIResponse{}, err
	}
	if c.Keyer == nil {
		return APIResponse{}, fmt.Errorf("NIP-96 upload signer required")
	}
	if len(opts.Data) == 0 {
		return APIResponse{}, fmt.Errorf("file data required")
	}
	if opts.Filename == "" {
		opts.Filename = "file"
	}
	if !contentTypeAllowed(opts.ContentType, c.Config.ContentTypes) {
		return APIResponse{}, fmt.Errorf("content type not supported")
	}
	if p, ok := c.Config.Plans["free"]; ok && p.MaxByteSize > 0 && int64(len(opts.Data)) > p.MaxByteSize {
		return APIResponse{}, fmt.Errorf("file exceeds server plan limit")
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", opts.Filename)
	if err != nil {
		return APIResponse{}, err
	}
	if _, err = part.Write(opts.Data); err != nil {
		return APIResponse{}, err
	}
	fields := map[string]string{"caption": opts.Caption, "alt": opts.Alt, "size": strconv.Itoa(len(opts.Data)), "media_type": opts.MediaType, "content_type": opts.ContentType}
	if opts.Expiration > 0 {
		fields["expiration"] = strconv.FormatInt(opts.Expiration, 10)
	}
	if opts.NoTransform {
		fields["no_transform"] = "true"
	}
	for k, v := range opts.Fields {
		fields[k] = v
	}
	for k, v := range fields {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	if err = mw.Close(); err != nil {
		return APIResponse{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.Config.APIURL, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	auth, err := c.authorization(ctx, http.MethodPost, c.Config.APIURL, opts.Data)
	if err != nil {
		return APIResponse{}, err
	}
	req.Header.Set("Authorization", auth)
	resp, err := noRedirectClient(c.HTTP).Do(req)
	if err != nil {
		return APIResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 && resp.StatusCode != 202 {
		return APIResponse{}, httpError(resp)
	}
	var result APIResponse
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result) != nil {
		return APIResponse{}, fmt.Errorf("invalid NIP-96 upload response")
	}
	sum := sha256.Sum256(opts.Data)
	hash := hex.EncodeToString(sum[:])
	if result.Status != "success" || result.NIP94Event == nil {
		return APIResponse{}, fmt.Errorf("NIP-96 upload failed: %s", result.Message)
	}
	if tag(result.NIP94Event.Tags, "url") == "" || tag(result.NIP94Event.Tags, "ox") != hash {
		return APIResponse{}, fmt.Errorf("NIP-96 response missing url or matching original hash")
	}
	if opts.NoTransform && tag(result.NIP94Event.Tags, "x") != hash {
		return APIResponse{}, fmt.Errorf("NIP-96 no_transform response hash mismatch")
	}
	return result, nil
}
func (c *Client) Download(ctx context.Context, hash, ext string) (*http.Response, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	base := c.Config.DownloadURL
	if base == "" {
		base = c.Config.APIURL
	}
	u, err := fileURL(base, hash, ext)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := noRedirectClient(c.HTTP).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		return nil, httpError(resp)
	}
	return resp, nil
}
func (c *Client) Delete(ctx context.Context, hash, ext string) (APIResponse, error) {
	if c.Keyer == nil {
		return APIResponse{}, fmt.Errorf("NIP-96 delete signer required")
	}
	u, err := fileURL(c.Config.APIURL, hash, ext)
	if err != nil {
		return APIResponse{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	auth, err := c.authorization(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return APIResponse{}, err
	}
	req.Header.Set("Authorization", auth)
	resp, err := noRedirectClient(c.HTTP).Do(req)
	if err != nil {
		return APIResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return APIResponse{}, httpError(resp)
	}
	var result APIResponse
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || result.Status != "success" {
		return APIResponse{}, fmt.Errorf("invalid NIP-96 delete response")
	}
	return result, nil
}
func (c *Client) List(ctx context.Context, page, count int) (FileList, error) {
	if c.Keyer == nil {
		return FileList{}, fmt.Errorf("NIP-96 list signer required")
	}
	u, err := url.Parse(c.Config.APIURL)
	if err != nil {
		return FileList{}, err
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("count", strconv.Itoa(count))
	u.RawQuery = q.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	auth, err := c.authorization(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return FileList{}, err
	}
	req.Header.Set("Authorization", auth)
	resp, err := noRedirectClient(c.HTTP).Do(req)
	if err != nil {
		return FileList{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return FileList{}, httpError(resp)
	}
	var result FileList
	if json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result) != nil {
		return FileList{}, fmt.Errorf("invalid NIP-96 file list")
	}
	return result, nil
}
func (c *Client) authorization(ctx context.Context, method, u string, payload []byte) (string, error) {
	_, header, err := nip98.Build(ctx, c.Keyer, nip98.BuildOptions{Method: method, URL: u, Body: payload})
	return header, err
}
func fileURL(base, hash, ext string) (string, error) {
	if err := validHash(hash); err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" {
		return "", fmt.Errorf("invalid NIP-96 URL")
	}
	ext = strings.TrimPrefix(ext, ".")
	name := hash
	if ext != "" {
		for _, r := range ext {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return "", fmt.Errorf("invalid extension")
			}
		}
		name += "." + ext
	}
	u.Path = path.Join(u.Path, name)
	return u.String(), nil
}
func validHash(hash string) error {
	if len(hash) != 64 || strings.ToLower(hash) != hash {
		return fmt.Errorf("invalid SHA-256 hash")
	}
	_, err := hex.DecodeString(hash)
	return err
}
func tag(tags nostr.Tags, name string) string {
	for _, t := range tags {
		if len(t) >= 2 && t[0] == name {
			return t[1]
		}
	}
	return ""
}
func contentTypeAllowed(got string, allowed []string) bool {
	if len(allowed) == 0 || got == "" {
		return true
	}
	for _, a := range allowed {
		if a == got || strings.HasSuffix(a, "/*") && strings.HasPrefix(got, strings.TrimSuffix(a, "*")) {
			return true
		}
	}
	return false
}
func httpError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("NIP-96 HTTP %s: %s", resp.Status, strings.TrimSpace(string(b)))
}

func BuildServerPreference(ctx context.Context, keyer nostr.Signer, servers []string) (nostr.Event, error) {
	if keyer == nil || len(servers) == 0 {
		return nostr.Event{}, fmt.Errorf("servers and signer required")
	}
	tags := nostr.Tags{}
	for _, server := range servers {
		u, err := url.Parse(server)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return nostr.Event{}, fmt.Errorf("invalid NIP-96 server")
		}
		tags = append(tags, nostr.Tag{"server", strings.TrimRight(server, "/")})
	}
	e := nostr.Event{Kind: KindServerPreference, CreatedAt: nostr.Now(), Tags: tags}
	if err := keyer.SignEvent(ctx, &e); err != nil {
		return nostr.Event{}, err
	}
	return e, nil
}
func ParseServerPreference(e nostr.Event) ([]string, error) {
	if e.Kind != KindServerPreference || !e.CheckID() || !e.VerifySignature() {
		return nil, fmt.Errorf("invalid NIP-96 server preference")
	}
	var servers []string
	for _, t := range e.Tags {
		if len(t) == 2 && t[0] == "server" {
			servers = append(servers, t[1])
		}
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("server tags required")
	}
	return servers, nil
}
