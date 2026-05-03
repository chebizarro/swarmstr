package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const ClawHubRegistryURL = "https://registry.clawhub.ai"

type ClawHubClient struct {
	httpClient *http.Client
	baseURL    string
}

type ClawHubPlugin struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Description  string           `json:"description"`
	Author       string           `json:"author"`
	Repository   string           `json:"repository"`
	Downloads    int              `json:"downloads"`
	Verified     bool             `json:"verified"`
	Capabilities []string         `json:"capabilities"`
	Package      string           `json:"package"`
	DistURL      string           `json:"distUrl"`
	OpenClaw     OpenClawMetadata `json:"openclaw"`
}

type OpenClawMetadata struct {
	Compat struct {
		PluginAPI string `json:"pluginApi"`
	} `json:"compat"`
	Build struct {
		OpenClawVersion string `json:"openclawVersion"`
	} `json:"build"`
}

func NewClawHubClient(baseURL string, httpClient *http.Client) *ClawHubClient {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = ClawHubRegistryURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &ClawHubClient{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

func (c *ClawHubClient) Search(ctx context.Context, query string) ([]ClawHubPlugin, error) {
	rel := "/v1/plugins"
	if q := strings.TrimSpace(query); q != "" {
		rel += "?q=" + url.QueryEscape(q)
	}
	var resp struct {
		Plugins []ClawHubPlugin `json:"plugins"`
	}
	if err := c.getJSON(ctx, rel, &resp); err != nil {
		return nil, err
	}
	return resp.Plugins, nil
}

func (c *ClawHubClient) GetPluginInfo(ctx context.Context, pluginID string) (*ClawHubPlugin, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return nil, fmt.Errorf("pluginID is required")
	}
	var p ClawHubPlugin
	if err := c.getJSON(ctx, path.Join("/v1/plugins", pluginID), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *ClawHubClient) Install(ctx context.Context, pluginID, version, installPath string) error {
	p, err := c.resolvePlugin(ctx, pluginID, version)
	if err != nil {
		return err
	}
	return installClawHubPlugin(ctx, p, installPath)
}

func (c *ClawHubClient) Update(ctx context.Context, pluginID, version, installPath string) error {
	p, err := c.resolvePlugin(ctx, pluginID, version)
	if err != nil {
		return err
	}
	return updateClawHubPlugin(ctx, p, installPath)
}

func (c *ClawHubClient) resolvePlugin(ctx context.Context, pluginID, version string) (*ClawHubPlugin, error) {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return nil, fmt.Errorf("pluginID is required")
	}
	rel := path.Join("/v1/plugins", pluginID)
	if v := strings.TrimSpace(version); v != "" {
		rel += "?version=" + url.QueryEscape(v)
	}
	var p ClawHubPlugin
	if err := c.getJSON(ctx, rel, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *ClawHubClient) getJSON(ctx context.Context, relPath string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+relPath, nil)
	if err != nil {
		return fmt.Errorf("create clawhub request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "metiq-plugin-installer/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("clawhub request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("clawhub request failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode clawhub response: %w", err)
	}
	return nil
}

func installClawHubPlugin(ctx context.Context, p *ClawHubPlugin, installPath string) error {
	if spec := strings.TrimSpace(p.Package); spec != "" {
		_, err := installNPM(ctx, spec, installPath)
		return err
	}
	if dist := strings.TrimSpace(p.DistURL); dist != "" {
		tmp, err := DownloadURL(ctx, dist)
		if err != nil {
			return err
		}
		defer removeFile(tmp)
		_, err = extractArchive(ctx, tmp, installPath)
		return err
	}
	return fmt.Errorf("clawhub plugin %q missing install package or distUrl", p.ID)
}

func updateClawHubPlugin(ctx context.Context, p *ClawHubPlugin, installPath string) error {
	if spec := strings.TrimSpace(p.Package); spec != "" {
		_, err := updateNPM(ctx, spec, installPath)
		return err
	}
	// For archive-style plugins, update is equivalent to reinstall latest.
	return installClawHubPlugin(ctx, p, installPath)
}

func removeFile(path string) {
	_ = os.Remove(path)
}
