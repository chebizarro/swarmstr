// taskqueue.go provides in-process persistent task queue tools for autonomous
// agent work management:
//
//   - task_add    → create a new task
//   - task_list   → list tasks (with optional status/priority filter)
//   - task_update → update task status or add notes
//   - task_remove → delete a task
//
// Tasks are stored as JSON in a package-level map and flushed to disk at the
// configured path.  They survive daemon restarts and are isolated per-agent
// by storage path.
package toolbuiltin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/agent"
)

// Task represents a unit of work in the agent's task queue.
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`             // "pending" | "in_progress" | "done" | "cancelled"
	Priority    string `json:"priority,omitempty"` // "high" | "medium" | "low"
	Notes       string `json:"notes,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// taskStore is the in-process task store.
type taskStore struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	filePath string
	seq      int
}

var defaultTaskStore = &taskStore{
	tasks: map[string]*Task{},
}

// InitTaskStore configures the task queue storage path and loads persisted tasks.
// Call once from main() after the data directory is known.
func InitTaskStore(path string) error {
	defaultTaskStore.mu.Lock()
	defer defaultTaskStore.mu.Unlock()
	defaultTaskStore.filePath = path
	return defaultTaskStore.loadLocked()
}

func (s *taskStore) loadLocked() error {
	if s.filePath == "" {
		return nil
	}
	raw, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return nil // fresh start
	}
	if err != nil {
		return err
	}
	var tasks []*Task
	if err := json.Unmarshal(raw, &tasks); err != nil {
		return err
	}
	for _, t := range tasks {
		s.tasks[t.ID] = t
		// Track sequence for ID generation.
		var n int
		if _, err := fmt.Sscanf(t.ID, "task-%d", &n); err == nil && n > s.seq {
			s.seq = n
		}
	}
	return nil
}

func (s *taskStore) saveLocked() {
	if s.filePath == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.filePath), 0755)
	tasks := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt < tasks[j].CreatedAt })
	raw, _ := json.MarshalIndent(tasks, "", "  ")
	_ = os.WriteFile(s.filePath, raw, 0644)
}

func (s *taskStore) nextID() string {
	s.seq++
	return fmt.Sprintf("task-%d", s.seq)
}

// ─── Tool definitions ─────────────────────────────────────────────────────────

// TaskAddDef is the ToolDefinition for task_add.
var TaskAddDef = agent.ToolDefinition{
	Name:        "task_add",
	Description: "Add a new task to the persistent task queue. Returns the task ID. Use to track discrete units of work that survive session restarts.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"title": {
				Type:        "string",
				Description: "Short, actionable title for the task, e.g. \"Research competitor pricing\".",
			},
			"description": {
				Type:        "string",
				Description: "Detailed description of what needs to be done.",
			},
			"priority": {
				Type:        "string",
				Description: "Task priority: \"high\", \"medium\" (default), or \"low\".",
				Enum:        []string{"high", "medium", "low"},
			},
		},
		Required: []string{"title"},
	},
}

// TaskListDef is the ToolDefinition for task_list.
var TaskListDef = agent.ToolDefinition{
	Name:        "task_list",
	Description: "List tasks in the queue. Filter by status or priority. Returns all tasks matching the filter, sorted by created_at descending.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"status": {
				Type:        "string",
				Description: "Filter by status: \"pending\", \"in_progress\", \"done\", or \"cancelled\". Omit to return all.",
				Enum:        []string{"pending", "in_progress", "done", "cancelled"},
			},
			"priority": {
				Type:        "string",
				Description: "Filter by priority: \"high\", \"medium\", or \"low\".",
				Enum:        []string{"high", "medium", "low"},
			},
		},
	},
}

// TaskUpdateDef is the ToolDefinition for task_update.
var TaskUpdateDef = agent.ToolDefinition{
	Name:        "task_update",
	Description: "Update the status, notes, or priority of an existing task.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Task ID to update (from task_add or task_list).",
			},
			"status": {
				Type:        "string",
				Description: "New status: \"pending\", \"in_progress\", \"done\", or \"cancelled\".",
				Enum:        []string{"pending", "in_progress", "done", "cancelled"},
			},
			"notes": {
				Type:        "string",
				Description: "Additional notes or progress update to append.",
			},
			"priority": {
				Type:        "string",
				Description: "Updated priority level.",
				Enum:        []string{"high", "medium", "low"},
			},
		},
		Required: []string{"id"},
	},
}

// TaskRemoveDef is the ToolDefinition for task_remove.
var TaskRemoveDef = agent.ToolDefinition{
	Name:        "task_remove",
	Description: "Permanently delete a task from the queue.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"id": {
				Type:        "string",
				Description: "Task ID to remove.",
			},
		},
		Required: []string{"id"},
	},
}

// ─── Tool functions ───────────────────────────────────────────────────────────

// TaskAddTool creates a new task.
func TaskAddTool(_ context.Context, args map[string]any) (string, error) {
	title := strings.TrimSpace(agent.ArgString(args, "title"))
	if title == "" {
		return "", fmt.Errorf("task_add: 'title' is required")
	}
	priority := agent.ArgString(args, "priority")
	if priority == "" {
		priority = "medium"
	}

	s := defaultTaskStore
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID()
	now := time.Now().Unix()
	t := &Task{
		ID:          id,
		Title:       title,
		Description: agent.ArgString(args, "description"),
		Status:      "pending",
		Priority:    priority,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.tasks[id] = t
	s.saveLocked()

	b, _ := json.Marshal(t)
	return string(b), nil
}

// TaskListTool lists tasks with optional filtering.
func TaskListTool(_ context.Context, args map[string]any) (string, error) {
	statusFilter := agent.ArgString(args, "status")
	priorityFilter := agent.ArgString(args, "priority")

	s := defaultTaskStore
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*Task
	for _, t := range s.tasks {
		if statusFilter != "" && t.Status != statusFilter {
			continue
		}
		if priorityFilter != "" && t.Priority != priorityFilter {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })

	b, _ := json.Marshal(map[string]any{"tasks": out, "count": len(out)})
	return string(b), nil
}

// TaskUpdateTool updates a task's status, priority, or notes.
func TaskUpdateTool(_ context.Context, args map[string]any) (string, error) {
	id := agent.ArgString(args, "id")
	if id == "" {
		return "", fmt.Errorf("task_update: 'id' is required")
	}

	s := defaultTaskStore
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tasks[id]
	if !ok {
		return "", fmt.Errorf("task_update: task %q not found", id)
	}
	if status := agent.ArgString(args, "status"); status != "" {
		t.Status = status
	}
	if priority := agent.ArgString(args, "priority"); priority != "" {
		t.Priority = priority
	}
	if notes := agent.ArgString(args, "notes"); notes != "" {
		if t.Notes != "" {
			t.Notes += "\n" + notes
		} else {
			t.Notes = notes
		}
	}
	t.UpdatedAt = time.Now().Unix()
	s.saveLocked()

	b, _ := json.Marshal(t)
	return string(b), nil
}

// TaskRemoveTool deletes a task.
func TaskRemoveTool(_ context.Context, args map[string]any) (string, error) {
	id := agent.ArgString(args, "id")
	if id == "" {
		return "", fmt.Errorf("task_remove: 'id' is required")
	}

	s := defaultTaskStore
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[id]; !ok {
		return "", fmt.Errorf("task_remove: task %q not found", id)
	}
	delete(s.tasks, id)
	s.saveLocked()
	return fmt.Sprintf("task %s removed", id), nil
}
