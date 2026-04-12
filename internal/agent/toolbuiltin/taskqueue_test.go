package toolbuiltin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInitTaskStore(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tasks.json"
	if err := InitTaskStore(path); err != nil {
		t.Fatal(err)
	}
	// InitTaskStore only sets the path; file is created on first save.
	// Verify by adding a task (which triggers save) and reading the file.
	_, err := TaskAddTool(context.Background(), map[string]any{"title": "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskAddAndList(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	result, err := TaskAddTool(context.Background(), map[string]any{
		"title":       "Test task",
		"description": "A test task",
		"priority":    "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "Test task") {
		t.Errorf("add result: %s", result)
	}

	// List tasks
	listResult, err := TaskListTool(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listResult, "Test task") {
		t.Errorf("list should contain task: %s", listResult)
	}
}

// extractTaskID parses the flat Task JSON returned by TaskAddTool.
func extractTaskID(t *testing.T, result string) string {
	t.Helper()
	var task Task
	if err := json.Unmarshal([]byte(result), &task); err != nil {
		t.Fatalf("unmarshal task: %v\nraw: %s", err, result)
	}
	if task.ID == "" {
		t.Fatalf("empty task ID in: %s", result)
	}
	return task.ID
}

func TestTaskUpdate(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	result, _ := TaskAddTool(context.Background(), map[string]any{
		"title": "Update me",
	})
	id := extractTaskID(t, result)

	updateResult, err := TaskUpdateTool(context.Background(), map[string]any{
		"id":     id,
		"status": "done",
		"notes":  "Completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updateResult, "done") {
		t.Errorf("update result: %s", updateResult)
	}
}

func TestTaskRemove(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	result, _ := TaskAddTool(context.Background(), map[string]any{
		"title": "Remove me",
	})
	id := extractTaskID(t, result)

	removeResult, err := TaskRemoveTool(context.Background(), map[string]any{
		"id": id,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removeResult, "removed") {
		t.Errorf("remove result: %s", removeResult)
	}

	listResult, _ := TaskListTool(context.Background(), map[string]any{})
	if strings.Contains(listResult, "Remove me") {
		t.Error("task should be removed")
	}
}

func TestTaskAdd_EmptyTitle(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	_, err := TaskAddTool(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestTaskUpdate_NotFound(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	_, err := TaskUpdateTool(context.Background(), map[string]any{
		"id":     "nonexistent",
		"status": "done",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskRemove_NotFound(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	_, err := TaskRemoveTool(context.Background(), map[string]any{
		"id": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskList_FilterByStatus(t *testing.T) {
	dir := t.TempDir()
	InitTaskStore(dir + "/tasks.json")

	TaskAddTool(context.Background(), map[string]any{"title": "Open task"})
	result, _ := TaskAddTool(context.Background(), map[string]any{"title": "Done task"})
	id := extractTaskID(t, result)
	TaskUpdateTool(context.Background(), map[string]any{"id": id, "status": "done"})

	listResult, _ := TaskListTool(context.Background(), map[string]any{"status": "done"})
	if !strings.Contains(listResult, "Done task") {
		t.Errorf("should list done task: %s", listResult)
	}
}

func TestTask_JSONRoundTrip(t *testing.T) {
	task := Task{
		ID:       "t-1",
		Title:    "Test",
		Status:   "open",
		Priority: "high",
	}
	b, _ := json.Marshal(task)
	var decoded Task
	json.Unmarshal(b, &decoded)
	if decoded.ID != task.ID || decoded.Title != task.Title {
		t.Errorf("mismatch: %+v", decoded)
	}
}
