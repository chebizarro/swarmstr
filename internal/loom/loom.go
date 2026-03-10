// Package loom implements the Loom decentralized compute marketplace protocol.
//
// Loom enables clients to execute arbitrary commands on remote workers via Nostr,
// with Cashu ecash payments and Blossom for stdout/stderr storage.
// Spec: https://github.com/loom-protocol/loom
//
// Nostr event kinds:
//
//	10100 – Worker Advertisement (replaceable)
//	5100  – Job Request
//	30100 – Job Status Update (parameterized replaceable, d-tag = job request event ID)
//	5101  – Job Result
//	5102  – Job Cancellation Request
package loom

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	nostr "fiatjaf.com/nostr"
)

// Event kinds.
const (
	KindWorkerAdvertisement = 10100
	KindJobRequest          = 5100
	KindJobStatus           = 30100
	KindJobResult           = 5101
	KindJobCancellation     = 5102
)

// Job status values.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusTimeout   = "timeout"
)

// Software describes an installed program on a worker.
type Software struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// PriceEntry describes a worker's pricing for a specific Cashu mint.
type PriceEntry struct {
	MintURL        string `json:"mint_url"`
	PricePerSecond string `json:"price_per_second"`
	Unit           string `json:"unit"`
}

// Worker represents a Loom worker advertisement (kind 10100).
type Worker struct {
	PubKey          string       `json:"pubkey"`
	Name            string       `json:"name,omitempty"`
	Description     string       `json:"description,omitempty"`
	MaxConcurrent   int          `json:"max_concurrent_jobs,omitempty"`
	Software        []Software   `json:"software,omitempty"`
	Architecture    string       `json:"architecture,omitempty"`
	DefaultShell    string       `json:"default_shell,omitempty"`
	Prices          []PriceEntry `json:"prices,omitempty"`
	MinDuration     int          `json:"min_duration,omitempty"` // seconds
	MaxDuration     int          `json:"max_duration,omitempty"` // seconds
	PreferredRelays []string     `json:"relays,omitempty"`
	EventID         string       `json:"event_id,omitempty"`
	CreatedAt       int64        `json:"created_at,omitempty"`
}

// JobRequest holds the parameters for a new Loom job (kind 5100).
type JobRequest struct {
	WorkerPubKey string
	Command      string
	Args         []string
	Env          map[string]string // plain environment variables
	Secrets      map[string]string // NIP-44 pre-encrypted secret env vars (caller handles encryption)
	Stdin        string            // piped to stdin (content field)
	Payment      string            // Cashu token (pubkey-locked to worker)
}

// JobStatus represents a Loom job status update (kind 30100).
type JobStatus struct {
	JobRequestID  string `json:"job_request_id"`
	Status        string `json:"status"`
	Log           string `json:"log,omitempty"`
	QueuePosition int    `json:"queue_position,omitempty"`
	ClientPubKey  string `json:"client_pubkey,omitempty"`
	CreatedAt     int64  `json:"created_at,omitempty"`
}

// JobResult represents a Loom job result (kind 5101).
type JobResult struct {
	JobRequestID string `json:"job_request_id"`
	Success      bool   `json:"success"`
	ExitCode     int    `json:"exit_code"`
	DurationSecs int    `json:"duration_seconds"`
	StdoutURL    string `json:"stdout_url"`
	StderrURL    string `json:"stderr_url"`
	ChangeToken  string `json:"change_token,omitempty"` // unused Cashu payment returned
	ErrorMsg     string `json:"error,omitempty"`
	WorkerPubKey string `json:"worker_pubkey,omitempty"`
	CreatedAt    int64  `json:"created_at,omitempty"`
}

// ListWorkers fetches available Loom workers (kind 10100).
func ListWorkers(ctx context.Context, pool *nostr.Pool, relays []string, limit int) ([]Worker, error) {
	if limit <= 0 {
		limit = 20
	}
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindWorkerAdvertisement)},
		Limit: limit,
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var workers []Worker
	seen := make(map[string]bool)
	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		id := re.Event.ID.Hex()
		if seen[id] {
			continue
		}
		seen[id] = true
		workers = append(workers, decodeWorkerEvent(re.Event))
	}
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].CreatedAt > workers[j].CreatedAt
	})
	return workers, nil
}

// SubmitJob publishes a Loom job request (kind 5100) and returns the event ID.
// The event ID serves as the job identifier for status and result queries.
func SubmitJob(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, req JobRequest) (string, error) {
	if req.WorkerPubKey == "" {
		return "", fmt.Errorf("loom: worker pubkey is required")
	}
	if req.Command == "" {
		return "", fmt.Errorf("loom: command is required")
	}
	if req.Payment == "" {
		return "", fmt.Errorf("loom: payment (Cashu token) is required")
	}

	tags := nostr.Tags{
		{"p", req.WorkerPubKey},
		{"cmd", req.Command},
		{"payment", req.Payment},
	}
	if len(req.Args) > 0 {
		argsTag := nostr.Tag{"args"}
		argsTag = append(argsTag, req.Args...)
		tags = append(tags, argsTag)
	}
	for k, v := range req.Env {
		tags = append(tags, nostr.Tag{"env", k, v})
	}
	for k, v := range req.Secrets {
		tags = append(tags, nostr.Tag{"secret", k, v})
	}

	evt := nostr.Event{
		Kind:      nostr.Kind(KindJobRequest),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   req.Stdin,
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("loom: sign job request: %w", err)
	}
	return publishEvent(ctx, pool, relays, evt)
}

// GetJobStatus fetches the latest status update for a job (kind 30100).
func GetJobStatus(ctx context.Context, pool *nostr.Pool, relays []string, jobRequestID string) (*JobStatus, error) {
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindJobStatus)},
		Tags:  nostr.TagMap{"e": []string{jobRequestID}},
		Limit: 1,
	}

	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		return decodeJobStatusEvent(re.Event), nil
	}
	return nil, fmt.Errorf("loom: no status found for job %s", jobRequestID)
}

// WaitForResult waits for a Loom job result (kind 5101).
func WaitForResult(ctx context.Context, pool *nostr.Pool, relays []string, jobRequestID string, timeout time.Duration) (*JobResult, error) {
	filter := nostr.Filter{
		Kinds: []nostr.Kind{nostr.Kind(KindJobResult)},
		Tags:  nostr.TagMap{"e": []string{jobRequestID}},
		Limit: 1,
	}

	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for re := range pool.SubscribeMany(ctx2, relays, filter, nostr.SubscriptionOptions{}) {
		return decodeJobResultEvent(re.Event), nil
	}
	return nil, fmt.Errorf("loom: timed out waiting for result of job %s", jobRequestID)
}

// CancelJob publishes a Loom job cancellation request (kind 5102).
func CancelJob(ctx context.Context, pool *nostr.Pool, keyer nostr.Keyer, relays []string, jobRequestID, workerPubKey string) (string, error) {
	tags := nostr.Tags{
		{"e", jobRequestID},
		{"p", workerPubKey},
	}
	evt := nostr.Event{
		Kind:      nostr.Kind(KindJobCancellation),
		CreatedAt: nostr.Now(),
		Tags:      tags,
		Content:   "",
	}
	if err := keyer.SignEvent(ctx, &evt); err != nil {
		return "", fmt.Errorf("loom: sign cancellation: %w", err)
	}
	return publishEvent(ctx, pool, relays, evt)
}

// ── decoders ──────────────────────────────────────────────────────────────────

func decodeWorkerEvent(ev nostr.Event) Worker {
	w := Worker{
		PubKey:    ev.PubKey.Hex(),
		EventID:   ev.ID.Hex(),
		CreatedAt: int64(ev.CreatedAt),
	}
	// Parse content JSON (name, description, max_concurrent_jobs).
	if ev.Content != "" {
		var content struct {
			Name          string `json:"name"`
			Description   string `json:"description"`
			MaxConcurrent int    `json:"max_concurrent_jobs"`
		}
		if err := json.Unmarshal([]byte(ev.Content), &content); err == nil {
			w.Name = content.Name
			w.Description = content.Description
			w.MaxConcurrent = content.MaxConcurrent
		}
	}
	// Parse tags.
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "S":
			sw := Software{Name: tag[1]}
			if len(tag) >= 3 {
				sw.Version = tag[2]
			}
			if len(tag) >= 4 {
				sw.Path = tag[3]
			}
			w.Software = append(w.Software, sw)
		case "A":
			w.Architecture = tag[1]
		case "price":
			if len(tag) >= 4 {
				w.Prices = append(w.Prices, PriceEntry{
					MintURL:        tag[1],
					PricePerSecond: tag[2],
					Unit:           tag[3],
				})
			}
		case "default_shell":
			w.DefaultShell = tag[1]
		case "min_duration":
			fmt.Sscanf(tag[1], "%d", &w.MinDuration)
		case "max_duration":
			fmt.Sscanf(tag[1], "%d", &w.MaxDuration)
		case "relay":
			w.PreferredRelays = append(w.PreferredRelays, tag[1])
		}
	}
	return w
}

func decodeJobStatusEvent(ev nostr.Event) *JobStatus {
	s := &JobStatus{
		Log:       ev.Content,
		CreatedAt: int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			s.JobRequestID = tag[1]
		case "status":
			s.Status = tag[1]
		case "p":
			s.ClientPubKey = tag[1]
		case "queue_position":
			fmt.Sscanf(tag[1], "%d", &s.QueuePosition)
		}
	}
	return s
}

func decodeJobResultEvent(ev nostr.Event) *JobResult {
	r := &JobResult{
		WorkerPubKey: ev.PubKey.Hex(),
		CreatedAt:    int64(ev.CreatedAt),
	}
	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "e":
			r.JobRequestID = tag[1]
		case "success":
			r.Success = tag[1] == "true"
		case "exit_code":
			fmt.Sscanf(tag[1], "%d", &r.ExitCode)
		case "duration":
			fmt.Sscanf(tag[1], "%d", &r.DurationSecs)
		case "stdout":
			r.StdoutURL = tag[1]
		case "stderr":
			r.StderrURL = tag[1]
		case "change":
			r.ChangeToken = tag[1]
		case "error":
			r.ErrorMsg = tag[1]
		}
	}
	return r
}

func publishEvent(ctx context.Context, pool *nostr.Pool, relays []string, evt nostr.Event) (string, error) {
	published := false
	var lastErr error
	for result := range pool.PublishMany(ctx, relays, evt) {
		if result.Error == nil {
			published = true
		} else {
			lastErr = fmt.Errorf("relay %s: %w", result.RelayURL, result.Error)
		}
	}
	if !published {
		if lastErr == nil {
			lastErr = fmt.Errorf("no relay accepted the event")
		}
		return "", lastErr
	}
	return evt.ID.Hex(), nil
}

// MarshalJSON serializes a value as indented JSON string.
func MarshalJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
