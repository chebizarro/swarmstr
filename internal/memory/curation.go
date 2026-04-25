// Package memory — Memory curation for building durable knowledge from raw memories.
//
// This module implements the "Advanced Memory Curation Layer" that promotes raw
// session memories into organized, reviewed, and compiled knowledge. It builds
// on the promotion system to add:
//
//   - Compiled knowledge pages (topic summaries with provenance)
//   - Backfill scanning for historical memory files
//   - Review artifacts and operator tools
//   - Scheduled maintenance jobs
//
// The curation process:
//  1. Identify candidate memories from promotion pipeline
//  2. Group related memories by topic/embedding similarity
//  3. Generate or update compiled topic pages
//  4. Track freshness and contradictions
//  5. Create review artifacts for operator oversight
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ─── Compiled Knowledge ──────────────────────────────────────────────────────

// CompiledTopic represents a curated topic page with consolidated knowledge.
type CompiledTopic struct {
	ID          string    `json:"id"`
	Topic       string    `json:"topic"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Claims      []Claim   `json:"claims,omitempty"`
	Sources     []Source  `json:"sources,omitempty"`
	LastUpdated int64     `json:"last_updated"`
	CreatedAt   int64     `json:"created_at"`
	Version     int       `json:"version"`
	ReviewedAt  int64     `json:"reviewed_at,omitempty"`
	ReviewedBy  string    `json:"reviewed_by,omitempty"`
	Status      string    `json:"status"` // draft, reviewed, stale
	Freshness   float64   `json:"freshness"`
}

// Claim represents a single piece of knowledge with provenance.
type Claim struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Confidence   float64  `json:"confidence"`
	SourceIDs    []string `json:"source_ids"`
	CreatedAt    int64    `json:"created_at"`
	LastVerified int64    `json:"last_verified,omitempty"`
	Supersedes   string   `json:"supersedes,omitempty"`   // ID of claim this replaces
	SupersededBy string   `json:"superseded_by,omitempty"` // ID of replacing claim
	Contradicts  []string `json:"contradicts,omitempty"`  // IDs of contradicting claims
}

// Source represents a memory that contributed to knowledge.
type Source struct {
	MemoryID    string  `json:"memory_id"`
	SessionID   string  `json:"session_id,omitempty"`
	Text        string  `json:"text"`
	Relevance   float64 `json:"relevance"`
	CreatedAt   int64   `json:"created_at"`
}

// ─── Review Artifacts ────────────────────────────────────────────────────────

// ReviewArtifact represents pending work for operator review.
type ReviewArtifact struct {
	ID          string         `json:"id"`
	Type        ReviewType     `json:"type"`
	Priority    int            `json:"priority"`
	Topic       string         `json:"topic,omitempty"`
	Candidates  []ReviewItem   `json:"candidates,omitempty"`
	Suggestion  string         `json:"suggestion,omitempty"`
	Status      ReviewStatus   `json:"status"`
	CreatedAt   int64          `json:"created_at"`
	ReviewedAt  int64          `json:"reviewed_at,omitempty"`
	ReviewedBy  string         `json:"reviewed_by,omitempty"`
	Decision    string         `json:"decision,omitempty"`
	Notes       string         `json:"notes,omitempty"`
}

// ReviewItem is a single item requiring review.
type ReviewItem struct {
	MemoryID   string  `json:"memory_id"`
	Text       string  `json:"text"`
	Score      float64 `json:"score"`
	Reason     string  `json:"reason"`
}

// ReviewType categorizes review artifacts.
type ReviewType string

const (
	ReviewTypePromotion     ReviewType = "promotion"
	ReviewTypeContradiction ReviewType = "contradiction"
	ReviewTypeStale         ReviewType = "stale"
	ReviewTypeMerge         ReviewType = "merge"
	ReviewTypeBackfill      ReviewType = "backfill"
)

// ReviewStatus tracks artifact lifecycle.
type ReviewStatus string

const (
	ReviewStatusPending  ReviewStatus = "pending"
	ReviewStatusApproved ReviewStatus = "approved"
	ReviewStatusRejected ReviewStatus = "rejected"
	ReviewStatusDeferred ReviewStatus = "deferred"
)

// ─── Curation Manager ────────────────────────────────────────────────────────

// CurationConfig configures memory curation behavior.
type CurationConfig struct {
	// Enabled toggles curation (default: true).
	Enabled bool `json:"enabled"`

	// TopicsDir is the directory for compiled topic pages.
	TopicsDir string `json:"topics_dir"`

	// ReviewsDir is the directory for review artifacts.
	ReviewsDir string `json:"reviews_dir"`

	// StalenessThresholdDays marks topics as stale after this many days.
	StalenessThresholdDays int `json:"staleness_threshold_days"`

	// MinClaimsForCompilation is the minimum claims needed before compilation.
	MinClaimsForCompilation int `json:"min_claims_for_compilation"`

	// MaxClaimsPerTopic limits claims per topic page.
	MaxClaimsPerTopic int `json:"max_claims_per_topic"`

	// AutoReview enables automatic review approval for high-confidence items.
	AutoReview bool `json:"auto_review"`

	// AutoReviewThreshold is the confidence threshold for auto-approval.
	AutoReviewThreshold float64 `json:"auto_review_threshold"`

	// BackfillBatchSize limits memories processed per backfill run.
	BackfillBatchSize int `json:"backfill_batch_size"`

	// CompileSummary enables LLM-based summary generation.
	CompileSummary bool `json:"compile_summary"`

	// SummaryModel is the LLM model for summaries.
	SummaryModel string `json:"summary_model,omitempty"`
}

// DefaultCurationConfig returns sensible defaults.
func DefaultCurationConfig() CurationConfig {
	return CurationConfig{
		Enabled:                 true,
		TopicsDir:               "topics",
		ReviewsDir:              "reviews",
		StalenessThresholdDays:  30,
		MinClaimsForCompilation: 3,
		MaxClaimsPerTopic:       100,
		AutoReview:              false,
		AutoReviewThreshold:     0.9,
		BackfillBatchSize:       50,
		CompileSummary:          false,
	}
}

// CurationManager orchestrates memory curation.
type CurationManager struct {
	mu        sync.RWMutex
	cfg       CurationConfig
	baseDir   string
	topics    map[string]*CompiledTopic
	reviews   map[string]*ReviewArtifact
	promotion *PromotionManager

	// Optional summarizer callback
	summarizer func(claims []Claim) (string, error)
}

// NewCurationManager creates a new curation manager.
func NewCurationManager(baseDir string, cfg CurationConfig, promotion *PromotionManager) *CurationManager {
	return &CurationManager{
		cfg:       cfg,
		baseDir:   baseDir,
		topics:    make(map[string]*CompiledTopic),
		reviews:   make(map[string]*ReviewArtifact),
		promotion: promotion,
	}
}

// SetSummarizer sets the callback for generating topic summaries.
func (m *CurationManager) SetSummarizer(fn func([]Claim) (string, error)) {
	m.summarizer = fn
}

// ─── Topic Management ────────────────────────────────────────────────────────

// GetTopic returns a compiled topic by ID.
func (m *CurationManager) GetTopic(topicID string) (*CompiledTopic, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	topic, ok := m.topics[topicID]
	return topic, ok
}

// ListTopics returns all compiled topics.
func (m *CurationManager) ListTopics() []*CompiledTopic {
	m.mu.RLock()
	defer m.mu.RUnlock()

	topics := make([]*CompiledTopic, 0, len(m.topics))
	for _, t := range m.topics {
		topics = append(topics, t)
	}

	sort.Slice(topics, func(i, j int) bool {
		return topics[i].LastUpdated > topics[j].LastUpdated
	})

	return topics
}

// GetOrCreateTopic returns an existing topic or creates a new one.
func (m *CurationManager) GetOrCreateTopic(topicName string) *CompiledTopic {
	m.mu.Lock()
	defer m.mu.Unlock()

	topicID := normalizeTopicID(topicName)
	if topic, ok := m.topics[topicID]; ok {
		return topic
	}

	now := time.Now().Unix()
	topic := &CompiledTopic{
		ID:          topicID,
		Topic:       topicName,
		Title:       strings.Title(strings.ReplaceAll(topicName, "_", " ")),
		CreatedAt:   now,
		LastUpdated: now,
		Version:     1,
		Status:      "draft",
		Freshness:   1.0,
	}

	m.topics[topicID] = topic
	return topic
}

// AddClaimToTopic adds a new claim to a topic.
func (m *CurationManager) AddClaimToTopic(topicID string, claim Claim) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	topic, ok := m.topics[topicID]
	if !ok {
		return fmt.Errorf("topic %q not found", topicID)
	}

	if m.cfg.MaxClaimsPerTopic > 0 && len(topic.Claims) >= m.cfg.MaxClaimsPerTopic {
		return fmt.Errorf("topic %q has reached max claims (%d)", topicID, m.cfg.MaxClaimsPerTopic)
	}

	// Generate claim ID if not set
	if claim.ID == "" {
		claim.ID = generateClaimID(claim.Text)
	}
	if claim.CreatedAt == 0 {
		claim.CreatedAt = time.Now().Unix()
	}

	topic.Claims = append(topic.Claims, claim)
	topic.LastUpdated = time.Now().Unix()
	topic.Version++
	topic.Status = "draft"

	return nil
}

// CompileTopic generates or updates the topic summary.
func (m *CurationManager) CompileTopic(ctx context.Context, topicID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	topic, ok := m.topics[topicID]
	if !ok {
		return fmt.Errorf("topic %q not found", topicID)
	}

	if len(topic.Claims) < m.cfg.MinClaimsForCompilation {
		return fmt.Errorf("topic %q has insufficient claims (%d < %d)",
			topicID, len(topic.Claims), m.cfg.MinClaimsForCompilation)
	}

	// Generate summary if summarizer available
	if m.cfg.CompileSummary && m.summarizer != nil {
		summary, err := m.summarizer(topic.Claims)
		if err == nil && summary != "" {
			topic.Summary = summary
		}
	} else {
		// Simple summary: concatenate high-confidence claims
		var summaryParts []string
		for _, c := range topic.Claims {
			if c.Confidence >= 0.7 && c.SupersededBy == "" {
				summaryParts = append(summaryParts, c.Text)
			}
		}
		if len(summaryParts) > 0 {
			topic.Summary = strings.Join(summaryParts, " ")
		}
	}

	topic.LastUpdated = time.Now().Unix()
	topic.Version++
	topic.Freshness = m.computeFreshness(topic)

	return nil
}

// computeFreshness calculates topic freshness (0.0-1.0).
func (m *CurationManager) computeFreshness(topic *CompiledTopic) float64 {
	if topic.LastUpdated == 0 {
		return 0.0
	}

	daysSinceUpdate := float64(time.Now().Unix()-topic.LastUpdated) / 86400.0
	halfLife := float64(m.cfg.StalenessThresholdDays)

	// Exponential decay
	freshness := 1.0 / (1.0 + daysSinceUpdate/halfLife)
	return freshness
}

// MarkTopicReviewed marks a topic as reviewed.
func (m *CurationManager) MarkTopicReviewed(topicID, reviewedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	topic, ok := m.topics[topicID]
	if !ok {
		return fmt.Errorf("topic %q not found", topicID)
	}

	topic.ReviewedAt = time.Now().Unix()
	topic.ReviewedBy = reviewedBy
	topic.Status = "reviewed"

	return nil
}

// ─── Review Artifacts ────────────────────────────────────────────────────────

// CreateReviewArtifact creates a new review artifact.
func (m *CurationManager) CreateReviewArtifact(artifact ReviewArtifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if artifact.ID == "" {
		artifact.ID = generateReviewID(artifact.Type, artifact.Topic)
	}
	if artifact.CreatedAt == 0 {
		artifact.CreatedAt = time.Now().Unix()
	}
	if artifact.Status == "" {
		artifact.Status = ReviewStatusPending
	}

	m.reviews[artifact.ID] = &artifact
	return nil
}

// ListReviewArtifacts returns pending review artifacts.
func (m *CurationManager) ListReviewArtifacts(status ReviewStatus) []*ReviewArtifact {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var artifacts []*ReviewArtifact
	for _, r := range m.reviews {
		if status == "" || r.Status == status {
			artifacts = append(artifacts, r)
		}
	}

	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Priority != artifacts[j].Priority {
			return artifacts[i].Priority > artifacts[j].Priority
		}
		return artifacts[i].CreatedAt > artifacts[j].CreatedAt
	})

	return artifacts
}

// ResolveReviewArtifact resolves a review artifact.
func (m *CurationManager) ResolveReviewArtifact(id, decision, reviewedBy, notes string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	artifact, ok := m.reviews[id]
	if !ok {
		return fmt.Errorf("review artifact %q not found", id)
	}

	switch decision {
	case "approve", "approved":
		artifact.Status = ReviewStatusApproved
	case "reject", "rejected":
		artifact.Status = ReviewStatusRejected
	case "defer", "deferred":
		artifact.Status = ReviewStatusDeferred
	default:
		return fmt.Errorf("invalid decision: %s", decision)
	}

	artifact.ReviewedAt = time.Now().Unix()
	artifact.ReviewedBy = reviewedBy
	artifact.Decision = decision
	artifact.Notes = notes

	return nil
}

// ─── Backfill ────────────────────────────────────────────────────────────────

// BackfillResult represents the result of a backfill operation.
type BackfillResult struct {
	FilesScanned     int      `json:"files_scanned"`
	MemoriesFound    int      `json:"memories_found"`
	CandidatesFound  int      `json:"candidates_found"`
	ReviewsCreated   int      `json:"reviews_created"`
	Errors           []string `json:"errors,omitempty"`
	DurationMs       int64    `json:"duration_ms"`
}

// BackfillFromDirectory scans historical memory files for curation candidates.
func (m *CurationManager) BackfillFromDirectory(ctx context.Context, dir string) (*BackfillResult, error) {
	if !m.cfg.Enabled {
		return &BackfillResult{}, nil
	}

	startTime := time.Now()
	result := &BackfillResult{}

	// Scan for markdown memory files
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		result.FilesScanned++

		// Read and parse memory file
		content, err := os.ReadFile(path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}

		// Extract memories from markdown
		memories := extractMemoriesFromMarkdown(string(content), path)
		result.MemoriesFound += len(memories)

		// Create review artifacts for promising candidates
		for _, mem := range memories {
			if mem.Confidence >= 0.6 || len(mem.Text) > 100 {
				result.CandidatesFound++

				if result.ReviewsCreated < m.cfg.BackfillBatchSize {
					artifact := ReviewArtifact{
						Type:     ReviewTypeBackfill,
						Topic:    mem.Topic,
						Priority: int(mem.Confidence * 10),
						Candidates: []ReviewItem{{
							MemoryID: mem.MemoryID,
							Text:     mem.Text,
							Score:    mem.Confidence,
							Reason:   "backfill candidate",
						}},
						Suggestion: fmt.Sprintf("Promote to topic: %s", mem.Topic),
					}

					if err := m.CreateReviewArtifact(artifact); err == nil {
						result.ReviewsCreated++
					}
				}
			}
		}

		return nil
	})

	result.DurationMs = time.Since(startTime).Milliseconds()

	if err != nil {
		result.Errors = append(result.Errors, err.Error())
	}

	return result, nil
}

// extractMemoriesFromMarkdown parses a markdown file for memory entries.
func extractMemoriesFromMarkdown(content, path string) []IndexedMemory {
	var memories []IndexedMemory

	lines := strings.Split(content, "\n")
	var currentTopic string
	var currentEntry strings.Builder

	for _, line := range lines {
		// Topic headers
		if strings.HasPrefix(line, "## ") {
			// Save previous entry
			if currentEntry.Len() > 0 {
				memories = append(memories, IndexedMemory{
					MemoryID:   generateMemoryID(),
					Topic:      currentTopic,
					Text:       strings.TrimSpace(currentEntry.String()),
					Unix:       time.Now().Unix(),
					Source:     path,
					Confidence: 0.5,
				})
				currentEntry.Reset()
			}
			currentTopic = normalizeTopicID(strings.TrimPrefix(line, "## "))
			continue
		}

		// Memory entries (list items or paragraphs)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			// Save previous entry
			if currentEntry.Len() > 0 {
				memories = append(memories, IndexedMemory{
					MemoryID:   generateMemoryID(),
					Topic:      currentTopic,
					Text:       strings.TrimSpace(currentEntry.String()),
					Unix:       time.Now().Unix(),
					Source:     path,
					Confidence: 0.5,
				})
				currentEntry.Reset()
			}
			currentEntry.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
		} else if line != "" && currentEntry.Len() > 0 {
			currentEntry.WriteString(" ")
			currentEntry.WriteString(line)
		}
	}

	// Save final entry
	if currentEntry.Len() > 0 {
		memories = append(memories, IndexedMemory{
			MemoryID:   generateMemoryID(),
			Topic:      currentTopic,
			Text:       strings.TrimSpace(currentEntry.String()),
			Unix:       time.Now().Unix(),
			Source:     path,
			Confidence: 0.5,
		})
	}

	return memories
}

// ─── Contradiction Detection ─────────────────────────────────────────────────

// ContradictionResult represents detected contradictions.
type ContradictionResult struct {
	Claim1    Claim   `json:"claim1"`
	Claim2    Claim   `json:"claim2"`
	Reason    string  `json:"reason"`
	Severity  float64 `json:"severity"` // 0.0-1.0
}

// DetectContradictions finds potentially contradicting claims in a topic.
// This is a simplified keyword-based approach - a full implementation would use embeddings.
func (m *CurationManager) DetectContradictions(topicID string) ([]ContradictionResult, error) {
	m.mu.RLock()
	topic, ok := m.topics[topicID]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("topic %q not found", topicID)
	}

	var contradictions []ContradictionResult

	// Simple heuristic: look for negation patterns
	negationPatterns := []string{"not ", "never ", "don't ", "doesn't ", "isn't ", "aren't ", "no "}

	for i, c1 := range topic.Claims {
		for j, c2 := range topic.Claims {
			if i >= j {
				continue
			}

			// Skip superseded claims
			if c1.SupersededBy != "" || c2.SupersededBy != "" {
				continue
			}

			// Check for negation patterns
			text1 := strings.ToLower(c1.Text)
			text2 := strings.ToLower(c2.Text)

			for _, neg := range negationPatterns {
				// If one claim contains a negation of something mentioned in the other
				if strings.Contains(text1, neg) || strings.Contains(text2, neg) {
					// Check for word overlap (very simple approach)
					words1 := strings.Fields(text1)
					words2 := strings.Fields(text2)

					overlap := 0
					for _, w1 := range words1 {
						for _, w2 := range words2 {
							if w1 == w2 && len(w1) > 3 {
								overlap++
							}
						}
					}

					if overlap >= 3 {
						contradictions = append(contradictions, ContradictionResult{
							Claim1:   c1,
							Claim2:   c2,
							Reason:   "Potential negation conflict",
							Severity: 0.5,
						})
						break
					}
				}
			}
		}
	}

	return contradictions, nil
}

// ─── Persistence ─────────────────────────────────────────────────────────────

// SaveTopics persists all topics to disk.
func (m *CurationManager) SaveTopics() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	topicsDir := filepath.Join(m.baseDir, m.cfg.TopicsDir)
	if err := os.MkdirAll(topicsDir, 0755); err != nil {
		return fmt.Errorf("create topics dir: %w", err)
	}

	for id, topic := range m.topics {
		data, err := json.MarshalIndent(topic, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal topic %s: %w", id, err)
		}

		path := filepath.Join(topicsDir, id+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("write topic %s: %w", id, err)
		}
	}

	return nil
}

// LoadTopics loads topics from disk.
func (m *CurationManager) LoadTopics() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	topicsDir := filepath.Join(m.baseDir, m.cfg.TopicsDir)
	entries, err := os.ReadDir(topicsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read topics dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(topicsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var topic CompiledTopic
		if err := json.Unmarshal(data, &topic); err != nil {
			continue
		}

		m.topics[topic.ID] = &topic
	}

	return nil
}

// SaveReviews persists pending reviews to disk.
func (m *CurationManager) SaveReviews() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	reviewsDir := filepath.Join(m.baseDir, m.cfg.ReviewsDir)
	if err := os.MkdirAll(reviewsDir, 0755); err != nil {
		return fmt.Errorf("create reviews dir: %w", err)
	}

	for id, review := range m.reviews {
		if review.Status != ReviewStatusPending {
			continue // Only save pending reviews
		}

		data, err := json.MarshalIndent(review, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal review %s: %w", id, err)
		}

		path := filepath.Join(reviewsDir, id+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("write review %s: %w", id, err)
		}
	}

	return nil
}

// LoadReviews loads pending reviews from disk.
func (m *CurationManager) LoadReviews() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	reviewsDir := filepath.Join(m.baseDir, m.cfg.ReviewsDir)
	entries, err := os.ReadDir(reviewsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read reviews dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(reviewsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var review ReviewArtifact
		if err := json.Unmarshal(data, &review); err != nil {
			continue
		}

		m.reviews[review.ID] = &review
	}

	return nil
}

// ─── Statistics ──────────────────────────────────────────────────────────────

// CurationStats provides overview statistics.
type CurationStats struct {
	TotalTopics      int            `json:"total_topics"`
	TotalClaims      int            `json:"total_claims"`
	PendingReviews   int            `json:"pending_reviews"`
	StaleTopics      int            `json:"stale_topics"`
	ReviewedTopics   int            `json:"reviewed_topics"`
	AverageFreshness float64        `json:"average_freshness"`
	TopicsByStatus   map[string]int `json:"topics_by_status"`
	ReviewsByType    map[string]int `json:"reviews_by_type"`
}

// Stats returns curation statistics.
func (m *CurationManager) Stats() CurationStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := CurationStats{
		TopicsByStatus: make(map[string]int),
		ReviewsByType:  make(map[string]int),
	}

	var totalFreshness float64
	for _, topic := range m.topics {
		stats.TotalTopics++
		stats.TotalClaims += len(topic.Claims)
		stats.TopicsByStatus[topic.Status]++

		if topic.Status == "reviewed" {
			stats.ReviewedTopics++
		}
		if topic.Freshness < 0.5 {
			stats.StaleTopics++
		}
		totalFreshness += topic.Freshness
	}

	if stats.TotalTopics > 0 {
		stats.AverageFreshness = totalFreshness / float64(stats.TotalTopics)
	}

	for _, review := range m.reviews {
		if review.Status == ReviewStatusPending {
			stats.PendingReviews++
		}
		stats.ReviewsByType[string(review.Type)]++
	}

	return stats
}

// ─── Helper Functions ────────────────────────────────────────────────────────

func normalizeTopicID(topic string) string {
	topic = strings.ToLower(topic)
	topic = strings.ReplaceAll(topic, " ", "_")
	topic = strings.ReplaceAll(topic, "-", "_")
	return strings.TrimSpace(topic)
}

func generateClaimID(text string) string {
	h := sha256.Sum256([]byte(text + fmt.Sprintf("%d", time.Now().UnixNano())))
	return "claim_" + hex.EncodeToString(h[:8])
}

func generateReviewID(reviewType ReviewType, topic string) string {
	h := sha256.Sum256([]byte(string(reviewType) + topic + fmt.Sprintf("%d", time.Now().UnixNano())))
	return "review_" + hex.EncodeToString(h[:8])
}

func generateMemoryID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	return "mem_" + hex.EncodeToString(h[:8])
}
