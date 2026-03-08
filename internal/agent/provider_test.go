package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8ByBytes_DoesNotSplitRunes(t *testing.T) {
	input := strings.Repeat("🙂", 5000)
	out := truncateUTF8ByBytes(input, 16*1024)
	if len(out) > 16*1024 {
		t.Fatalf("len(out) = %d, want <= %d", len(out), 16*1024)
	}
	if !utf8.ValidString(out) {
		t.Fatal("output is not valid UTF-8")
	}
}

func TestTruncateUTF8ByBytes_PreservesASCIIPrefix(t *testing.T) {
	input := "hello world"
	out := truncateUTF8ByBytes(input, 5)
	if out != "hello" {
		t.Fatalf("out = %q, want %q", out, "hello")
	}
}

func TestOpenAIChatProvider_Stream_LargeSSEChunk(t *testing.T) {
	longText := strings.Repeat("x", 120*1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", longText)
		_, _ = fmt.Fprintln(w, "data: [DONE]")
	}))
	defer srv.Close()

	p := &OpenAIChatProvider{BaseURL: srv.URL, Model: "gpt-4o", Client: srv.Client()}
	res, err := p.Stream(context.Background(), Turn{UserText: "hi"}, nil)
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}
	if len(res.Text) != len(longText) {
		t.Fatalf("streamed text length=%d want=%d", len(res.Text), len(longText))
	}
}
