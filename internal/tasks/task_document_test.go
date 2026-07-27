package tasks

import (
	"bytes"
	"strings"
	"testing"
)

func TestTaskDocumentCanonicalAndBeadsRoundTrip(t *testing.T) {
	input := []byte(`{
		"schema_version":"cascadia.task-state.v2",
		"id":"task-1",
		"title":"Ship task events",
		"description":"Implement the convention",
		"status":"blocked",
		"priority":1,
		"assignee":"agent-a",
		"labels":["nostr","tasks"],
		"dependencies":[{"issue_id":"task-1","depends_on_id":"task-0","type":"discovered-from"}],
		"claimed_at":"2026-07-27T12:00:00-04:00",
		"blocked_at":"2026-07-27T16:30:00Z",
		"metadata":{
			"cascadia.claim.origin_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"cascadia.claim.origin_pubkey":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		},
		"review":{"required":true,"state":"requested"}
	}`)
	doc, err := ParseTaskDocument(input)
	if err != nil {
		t.Fatalf("ParseTaskDocument: %v", err)
	}
	if doc.ClaimedAt != "2026-07-27T16:00:00Z" {
		t.Fatalf("claimed_at = %q", doc.ClaimedAt)
	}
	var out bytes.Buffer
	if err := EncodeBeadsJSONL(&out, []TaskDocument{doc}); err != nil {
		t.Fatalf("EncodeBeadsJSONL: %v", err)
	}
	if !strings.Contains(out.String(), `"_type":"issue"`) || strings.Contains(out.String(), "schema_version") {
		t.Fatalf("unexpected beads output: %s", out.String())
	}
	imported, err := DecodeBeadsJSONL(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatalf("DecodeBeadsJSONL: %v", err)
	}
	if len(imported) != 1 || imported[0].Metadata[ClaimOriginIDMetaKey] != doc.Metadata[ClaimOriginIDMetaKey] {
		t.Fatalf("claim metadata did not round-trip: %#v", imported)
	}
	if string(imported[0].Review) != string(doc.Review) {
		t.Fatalf("review extension did not round-trip: %s != %s", imported[0].Review, doc.Review)
	}
}

func TestDecodeBeadsLegacyAliases(t *testing.T) {
	input := `{"_type":"issue","id":"legacy","title":"Legacy","body":"old body","status":"open","priority":"P9","created":"2026-07-27T00:00:00Z","dependsOn":["root"]}`
	docs, err := DecodeBeadsJSONL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeBeadsJSONL: %v", err)
	}
	if len(docs) != 1 || docs[0].Priority != 4 || docs[0].Description != "old body" {
		t.Fatalf("unexpected legacy projection: %#v", docs)
	}
	if len(docs[0].Dependencies) != 1 || docs[0].Dependencies[0].Type != "blocks" {
		t.Fatalf("legacy dependency not promoted: %#v", docs[0].Dependencies)
	}
}

func TestParseTaskDocumentRejectsUnknownAndDuplicateDependency(t *testing.T) {
	unknown := []byte(`{"schema_version":"cascadia.task-state.v2","id":"x","title":"X","status":"open","priority":2,"surprise":true}`)
	if _, err := ParseTaskDocument(unknown); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	duplicate := []byte(`{"schema_version":"cascadia.task-state.v2","id":"x","title":"X","status":"open","priority":2,"dependencies":[{"issue_id":"x","depends_on_id":"y","type":"blocks"},{"issue_id":"x","depends_on_id":"y","type":"blocks"}]}`)
	if _, err := ParseTaskDocument(duplicate); err == nil {
		t.Fatal("expected duplicate dependency rejection")
	}
}
