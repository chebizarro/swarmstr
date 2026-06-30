package channels

import "testing"

func TestBuildMediaPayload(t *testing.T) {
	payload := BuildMediaPayload([]MediaPayloadInput{
		{Path: "https://cdn.example/a.png", ContentType: "image/png"},
		{Path: "/tmp/b.txt"},
	}, true)
	if payload.MediaPath != "https://cdn.example/a.png" || payload.MediaType != "image/png" {
		t.Fatalf("unexpected single media fields: %+v", payload)
	}
	if len(payload.MediaPaths) != 2 || len(payload.MediaTypes) != 2 || payload.MediaTypes[1] != "" {
		t.Fatalf("expected cardinal media lists, got %+v", payload)
	}
}

func TestValidateMediaPayload_AllowsValidMedia(t *testing.T) {
	err := ValidateMediaPayload([]MediaPayloadInput{{
		Path:        "https://cdn.example/a.png",
		ContentType: "image/png",
		SizeBytes:   1024,
	}}, MediaLimits{MaxBytes: 2048, AllowedMIMEs: []string{"image/*"}})
	if err != nil {
		t.Fatalf("expected valid media, got %v", err)
	}
}

func TestValidateMediaPayload_DeniesOversize(t *testing.T) {
	err := ValidateMediaPayload([]MediaPayloadInput{{Path: "/tmp/a.png", ContentType: "image/png", SizeBytes: 4096}}, MediaLimits{MaxBytes: 1024})
	if err == nil {
		t.Fatal("expected oversize error")
	}
}

func TestValidateMediaPayload_DeniesMIME(t *testing.T) {
	err := ValidateMediaPayload([]MediaPayloadInput{{Path: "/tmp/a.exe", ContentType: "application/x-msdownload"}}, MediaLimits{AllowedMIMEs: []string{"image/*"}})
	if err == nil {
		t.Fatal("expected MIME denial")
	}
}

func TestValidateMediaPayload_DeniesTooManyItems(t *testing.T) {
	items := []MediaPayloadInput{{Path: "/tmp/1"}, {Path: "/tmp/2"}}
	err := ValidateMediaPayload(items, MediaLimits{MaxItems: 1})
	if err == nil {
		t.Fatal("expected too many items error")
	}
}

func TestResolveMediaMaxBytes(t *testing.T) {
	if got := ResolveMediaMaxBytes(8, 25); got != 8*MB {
		t.Fatalf("expected channel limit, got %d", got)
	}
	if got := ResolveMediaMaxBytes(0, 25); got != 25*MB {
		t.Fatalf("expected default limit, got %d", got)
	}
}
