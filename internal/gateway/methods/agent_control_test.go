package methods

import (
	"context"
	"testing"

	"metiq/internal/store/state"
)

func newAgentControlRepo() *state.DocsRepository {
	return state.NewDocsRepository(newTaskControlTestStore(), "author")
}

func TestDefaultAgentID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "main"},
		{"  ", "main"},
		{"main", "main"},
		{"Main", "main"},
		{"MAIN", "main"},
		{"worker", "worker"},
		{" Worker ", "Worker"},
	}
	for _, tt := range tests {
		got := DefaultAgentID(tt.in)
		if got != tt.want {
			t.Errorf("DefaultAgentID(%q) = %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsKnownAgentID_MainAlwaysOK(t *testing.T) {
	repo := newAgentControlRepo()
	if err := IsKnownAgentID(context.Background(), repo, ""); err != nil {
		t.Fatalf("empty id (main) should be ok: %v", err)
	}
	if err := IsKnownAgentID(context.Background(), repo, "main"); err != nil {
		t.Fatalf("main should be ok: %v", err)
	}
}

func TestIsKnownAgentID_UnknownReturnsError(t *testing.T) {
	repo := newAgentControlRepo()
	err := IsKnownAgentID(context.Background(), repo, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestIsKnownAgentID_DeletedReturnsError(t *testing.T) {
	repo := newAgentControlRepo()
	ctx := context.Background()
	if _, err := repo.PutAgent(ctx, "deleted-agent", state.AgentDoc{
		Version: 1, AgentID: "deleted-agent", Name: "Gone", Deleted: true,
	}); err != nil {
		t.Fatal(err)
	}
	err := IsKnownAgentID(ctx, repo, "deleted-agent")
	if err == nil {
		t.Fatal("expected error for deleted agent")
	}
}

func TestIsKnownAgentID_ExistingAgentOK(t *testing.T) {
	repo := newAgentControlRepo()
	ctx := context.Background()
	if _, err := repo.PutAgent(ctx, "worker", state.AgentDoc{
		Version: 1, AgentID: "worker", Name: "Worker",
	}); err != nil {
		t.Fatal(err)
	}
	if err := IsKnownAgentID(ctx, repo, "worker"); err != nil {
		t.Fatalf("existing agent should be ok: %v", err)
	}
}

func TestListAgents(t *testing.T) {
	repo := newAgentControlRepo()
	ctx := context.Background()
	for _, id := range []string{"a1", "a2"} {
		if _, err := repo.PutAgent(ctx, id, state.AgentDoc{
			Version: 1, AgentID: id, Name: id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := ListAgents(ctx, repo, 10)
	if err != nil {
		t.Fatal(err)
	}
	agents, ok := result["agents"].([]state.AgentDoc)
	if !ok {
		t.Fatalf("expected []AgentDoc, got %T", result["agents"])
	}
	if len(agents) < 2 {
		t.Fatalf("expected at least 2 agents, got %d", len(agents))
	}
}

func TestAgentFilesCRUD(t *testing.T) {
	repo := newAgentControlRepo()
	ctx := context.Background()

	if _, err := repo.PutAgent(ctx, "test-agent", state.AgentDoc{
		Version: 1, AgentID: "test-agent", Name: "Test",
	}); err != nil {
		t.Fatal(err)
	}

	result, err := SetAgentFile(ctx, repo, "test-agent", "readme.md", "# Hello")
	if err != nil {
		t.Fatalf("SetAgentFile: %v", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("expected ok=true, got %v", result)
	}

	getResult, err := GetAgentFile(ctx, repo, "test-agent", "readme.md")
	if err != nil {
		t.Fatalf("GetAgentFile: %v", err)
	}
	file, _ := getResult["file"].(map[string]any)
	if missing, _ := file["missing"].(bool); missing {
		t.Fatal("expected file to exist")
	}
	if content, _ := file["content"].(string); content != "# Hello" {
		t.Fatalf("content = %q want # Hello", content)
	}

	getResult2, err := GetAgentFile(ctx, repo, "test-agent", "nonexistent.md")
	if err != nil {
		t.Fatalf("GetAgentFile missing: %v", err)
	}
	file2, _ := getResult2["file"].(map[string]any)
	if missing, _ := file2["missing"].(bool); !missing {
		t.Fatal("expected missing=true for nonexistent file")
	}

	listResult, err := ListAgentFiles(ctx, repo, "test-agent", 10)
	if err != nil {
		t.Fatalf("ListAgentFiles: %v", err)
	}
	files, _ := listResult["files"].([]map[string]any)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if name, _ := files[0]["name"].(string); name != "readme.md" {
		t.Fatalf("file name = %q want readme.md", name)
	}
}
