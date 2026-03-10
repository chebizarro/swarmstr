// Package toolbuiltin – Blossom blob storage tools.
//
// Registers: blossom_upload, blossom_download, blossom_list, blossom_delete, blossom_mirror
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	nostr "fiatjaf.com/nostr"

	"swarmstr/internal/agent"
	"swarmstr/internal/blossom"
)

// BlossomToolOpts configures the Blossom tools.
type BlossomToolOpts struct {
	Keyer         nostr.Keyer
	DefaultServer string   // default Blossom server URL
	AllowedRoots  []string // allowed local file path prefixes for upload
}

// RegisterBlossomTools registers all Blossom tools into the given registry.
func RegisterBlossomTools(tools *agent.ToolRegistry, opts BlossomToolOpts) {
	makeClient := func(ctx context.Context) (*blossom.Client, error) {
		if opts.Keyer == nil {
			return nil, fmt.Errorf("blossom: no signing key configured")
		}
		return blossom.NewClient(ctx, opts.Keyer)
	}

	resolveServer := func(args map[string]any) string {
		if v, ok := args["server_url"].(string); ok && v != "" {
			return strings.TrimRight(v, "/")
		}
		return strings.TrimRight(opts.DefaultServer, "/")
	}

	// blossom_upload: upload a local file or raw content to a Blossom server.
	tools.Register("blossom_upload", func(ctx context.Context, args map[string]any) (string, error) {
		client, err := makeClient(ctx)
		if err != nil {
			return "", err
		}
		serverURL := resolveServer(args)
		if serverURL == "" {
			return "", fmt.Errorf("blossom_upload: server_url is required")
		}
		mimeType, _ := args["mime_type"].(string)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		var data []byte
		if path, ok := args["path"].(string); ok && path != "" {
			// Guard against directory traversal.
			if !isPathAllowed(path, opts.AllowedRoots) {
				return "", fmt.Errorf("blossom_upload: path %q is outside allowed roots", path)
			}
			data, err = os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("blossom_upload: read file: %w", err)
			}
			// Auto-detect mime type from extension if not provided.
			if mimeType == "application/octet-stream" {
				mimeType = mimeFromExt(filepath.Ext(path))
			}
		} else if content, ok := args["content"].(string); ok && content != "" {
			data = []byte(content)
		} else {
			return "", fmt.Errorf("blossom_upload: either path or content is required")
		}

		desc, err := client.Upload(ctx, serverURL, data, mimeType)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(desc)
		return string(out), nil
	})

	// blossom_download: download a blob by SHA256 hash.
	tools.Register("blossom_download", func(ctx context.Context, args map[string]any) (string, error) {
		client, err := makeClient(ctx)
		if err != nil {
			return "", err
		}
		serverURL := resolveServer(args)
		sha256Hex, _ := args["sha256"].(string)
		if sha256Hex == "" {
			return "", fmt.Errorf("blossom_download: sha256 is required")
		}

		data, contentType, err := client.Download(ctx, serverURL, sha256Hex)
		if err != nil {
			return "", err
		}

		// If output_path is given, write the file.
		if outPath, ok := args["output_path"].(string); ok && outPath != "" {
			if !isPathAllowed(outPath, opts.AllowedRoots) {
				return "", fmt.Errorf("blossom_download: output_path %q is outside allowed roots", outPath)
			}
			if err := os.WriteFile(outPath, data, 0644); err != nil {
				return "", fmt.Errorf("blossom_download: write file: %w", err)
			}
			out, _ := json.Marshal(map[string]any{
				"ok":           true,
				"sha256":       sha256Hex,
				"size":         len(data),
				"content_type": contentType,
				"saved_to":     outPath,
			})
			return string(out), nil
		}

		// Return content inline for small text blobs; otherwise return metadata.
		result := map[string]any{
			"sha256":       sha256Hex,
			"size":         len(data),
			"content_type": contentType,
		}
		if strings.HasPrefix(contentType, "text/") && len(data) <= 32*1024 {
			result["content"] = string(data)
		} else {
			result["note"] = "blob is binary or too large to inline; provide output_path to save"
		}
		out, _ := json.Marshal(result)
		return string(out), nil
	})

	// blossom_list: list blobs for a pubkey on a server.
	tools.Register("blossom_list", func(ctx context.Context, args map[string]any) (string, error) {
		client, err := makeClient(ctx)
		if err != nil {
			return "", err
		}
		serverURL := resolveServer(args)
		pubkeyHex, _ := args["pubkey"].(string)
		if pubkeyHex == "" {
			// Default to own pubkey.
			pk, pkErr := opts.Keyer.GetPublicKey(ctx)
			if pkErr != nil {
				return "", fmt.Errorf("blossom_list: pubkey required: %w", pkErr)
			}
			pubkeyHex = pk.Hex()
		}
		blobs, err := client.List(ctx, serverURL, pubkeyHex)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"pubkey": pubkeyHex, "blobs": blobs, "count": len(blobs)})
		return string(out), nil
	})

	// blossom_delete: delete a blob by SHA256 hash.
	tools.Register("blossom_delete", func(ctx context.Context, args map[string]any) (string, error) {
		client, err := makeClient(ctx)
		if err != nil {
			return "", err
		}
		serverURL := resolveServer(args)
		sha256Hex, _ := args["sha256"].(string)
		if sha256Hex == "" {
			return "", fmt.Errorf("blossom_delete: sha256 is required")
		}
		if err := client.Delete(ctx, serverURL, sha256Hex); err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"ok": true, "sha256": sha256Hex})
		return string(out), nil
	})

	// blossom_mirror: mirror a blob from one server to another.
	tools.Register("blossom_mirror", func(ctx context.Context, args map[string]any) (string, error) {
		client, err := makeClient(ctx)
		if err != nil {
			return "", err
		}
		targetServer := resolveServer(args)
		sourceURL, _ := args["source_url"].(string)
		sha256Hex, _ := args["sha256"].(string)
		if sourceURL == "" {
			return "", fmt.Errorf("blossom_mirror: source_url is required")
		}
		desc, err := client.Mirror(ctx, targetServer, sha256Hex, sourceURL)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(desc)
		return string(out), nil
	})
}

// mimeFromExt returns a basic MIME type from a file extension.
func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".pdf":
		return "application/pdf"
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// isPathAllowed checks whether path is under one of allowedRoots.
func isPathAllowed(path string, allowedRoots []string) bool {
	if len(allowedRoots) == 0 {
		return true
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for _, root := range allowedRoots {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		if strings.HasPrefix(abs, absRoot+string(filepath.Separator)) || abs == absRoot {
			return true
		}
	}
	return false
}
