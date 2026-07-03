package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAIProvider_JSONRequestBodyAndResponse asserts the exact outbound JSON
// request shape for text-to-image generation and parses a realistic OpenAI
// Images response (data[].b64_json).
func TestOpenAIProvider_JSONRequestBodyAndResponse(t *testing.T) {
	var body map[string]any
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type: %q", ct)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "gpt-image-1",
			"data": []map[string]any{
				{"b64_json": base64.StdEncoding.EncodeToString([]byte("PNGDATA")), "mime": "image/png", "width": 1024, "height": 1024},
			},
		})
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "sk-test", BaseURL: srv.URL, Model: "gpt-image-1"}
	res, err := p.Generate(context.Background(), ImageGenerationRequest{
		Prompt:         "a red fox",
		N:              2,
		Size:           "1024x1024",
		Quality:        "high",
		Format:         "png",
		NegativePrompt: "blurry",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// ── Request body assertions ──
	if gotPath != "/images/generations" {
		t.Errorf("path: %q", gotPath)
	}
	if body["model"] != "gpt-image-1" {
		t.Errorf("model: %v", body["model"])
	}
	if body["prompt"] != "a red fox" {
		t.Errorf("prompt: %v", body["prompt"])
	}
	if n, ok := body["n"].(float64); !ok || n != 2 {
		t.Errorf("n: %v", body["n"])
	}
	if body["size"] != "1024x1024" {
		t.Errorf("size: %v", body["size"])
	}
	if body["quality"] != "high" {
		t.Errorf("quality: %v", body["quality"])
	}
	if body["output_format"] != "png" {
		t.Errorf("output_format: %v", body["output_format"])
	}
	if body["negative_prompt"] != "blurry" {
		t.Errorf("negative_prompt: %v", body["negative_prompt"])
	}

	// ── Response parsing assertions ──
	if res.Provider != "openai" {
		t.Errorf("provider: %q", res.Provider)
	}
	if len(res.Images) != 1 {
		t.Fatalf("expected 1 image, got %#v", res.Images)
	}
	img := res.Images[0]
	if img.Mime != "image/png" || img.Width != 1024 || img.Height != 1024 {
		t.Errorf("unexpected image metadata: %#v", img)
	}
	decoded, derr := base64.StdEncoding.DecodeString(img.Base64)
	if derr != nil || string(decoded) != "PNGDATA" {
		t.Errorf("decoded base64=%q err=%v", decoded, derr)
	}
}

// TestOpenAIProvider_MultipartEditRequest asserts that edit/variation requests
// are sent as multipart form-data with the expected fields and image part, and
// parses a URL-style response.
func TestOpenAIProvider_MultipartEditRequest(t *testing.T) {
	var fields map[string]string
	var imageBytes []byte
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Fatalf("expected multipart content-type, got %q (err=%v)", r.Header.Get("Content-Type"), err)
		}
		fields = map[string]string{}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, perr := mr.NextPart()
			if perr == io.EOF {
				break
			}
			if perr != nil {
				t.Fatalf("read part: %v", perr)
			}
			data, _ := io.ReadAll(part)
			if part.FormName() == "image" {
				imageBytes = data
				if part.FileName() == "" {
					t.Error("expected image part to have a filename")
				}
			} else {
				fields[part.FormName()] = string(data)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"url": "https://cdn.example/out.png"}},
		})
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "sk-test", BaseURL: srv.URL, Model: "gpt-image-1"}
	res, err := p.Generate(context.Background(), ImageGenerationRequest{
		Prompt:      "make it winter",
		Mode:        "edit",
		N:           1,
		Size:        "1024x1024",
		SourceImage: &SourceImage{Base64: base64.StdEncoding.EncodeToString([]byte("SRCIMG")), Mime: "image/png"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gotPath != "/images/edits" {
		t.Errorf("path: %q", gotPath)
	}
	if fields["model"] != "gpt-image-1" || fields["prompt"] != "make it winter" ||
		fields["n"] != "1" || fields["size"] != "1024x1024" {
		t.Errorf("unexpected multipart fields: %#v", fields)
	}
	if string(imageBytes) != "SRCIMG" {
		t.Errorf("image bytes: %q", imageBytes)
	}
	if len(res.Images) != 1 || res.Images[0].URL != "https://cdn.example/out.png" {
		t.Errorf("unexpected result images: %#v", res.Images)
	}
}

// TestOpenAIProvider_HTTPErrorResponse verifies non-2xx responses surface as errors.
func TestOpenAIProvider_HTTPErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	p := &OpenAIProvider{APIKey: "sk-test", BaseURL: srv.URL, Model: "gpt-image-1"}
	_, err := p.Generate(context.Background(), ImageGenerationRequest{Prompt: "x", N: 1})
	if err == nil {
		t.Fatal("expected HTTP error")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("unexpected error: %v", err)
	}
}
