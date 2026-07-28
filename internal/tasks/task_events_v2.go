package tasks

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/events"
)

const TaskCollectionKind = int(events.NIP51_TASK_COLLECTION)

// ClaimOrigin identifies the immutable winning claim lineage.
type ClaimOrigin struct {
	EventID   string `json:"event_id"`
	Pubkey    string `json:"pubkey"`
	CreatedAt int64  `json:"created_at"`
	Assignee  string `json:"assignee"`
}

// TaskEventHead is one validated trusted author's current task snapshot.
type TaskEventHead struct {
	Event        nostr.Event
	Task         TaskDocument
	Claim        *ClaimOrigin
	InitialClaim bool
}

// TaskValidationPolicy controls trust and clock-skew validation.
type TaskValidationPolicy struct {
	TrustedTaskAuthors       []string
	TrustedCollectionAuthors []string
	MaxFutureSkew            time.Duration
	Now                      func() time.Time
}

func (p TaskValidationPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p TaskValidationPolicy) taskTrust() map[string]struct{} {
	return normalizedPubkeySet(p.TrustedTaskAuthors)
}

func (p TaskValidationPolicy) collectionTrust() map[string]struct{} {
	return normalizedPubkeySet(p.TrustedCollectionAuthors)
}

// BuildTaskStateEvent creates an unsigned complete kind-30900 event.
func BuildTaskStateEvent(doc TaskDocument, createdAt time.Time) (nostr.Event, error) {
	normalized, err := NormalizeTaskDocument(doc)
	if err != nil {
		return nostr.Event{}, err
	}
	normalized.SchemaVersion = TaskStateSchemaV2
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	content, err := EncodeTaskDocument(normalized)
	if err != nil {
		return nostr.Event{}, err
	}
	tags := nostr.Tags{
		{"d", "task:" + normalized.ID},
		{"domain", "task"},
		{"schema", TaskStateSchemaV2},
		{"status", normalized.Status},
		{"priority", fmt.Sprintf("P%d", normalized.Priority)},
	}
	if normalized.Assignee != "" {
		tags = append(tags, nostr.Tag{"assignee", normalized.Assignee})
	}
	labels := append([]string(nil), normalized.Labels...)
	sort.Strings(labels)
	for _, label := range labels {
		tags = append(tags, nostr.Tag{"t", label})
	}
	depIDs := make([]string, 0, len(normalized.Dependencies))
	seenDeps := map[string]struct{}{}
	for _, dep := range normalized.Dependencies {
		if _, ok := seenDeps[dep.DependsOnID]; ok {
			continue
		}
		seenDeps[dep.DependsOnID] = struct{}{}
		depIDs = append(depIDs, dep.DependsOnID)
	}
	sort.Strings(depIDs)
	for _, depID := range depIDs {
		tags = append(tags, nostr.Tag{"depends-on", "task:" + depID})
	}
	if normalized.Repository != "" {
		tags = append(tags, nostr.Tag{"a", normalized.Repository, "", "nip34-repo"})
	}
	return nostr.Event{
		Kind:      nostr.Kind(events.CAS_CP_STATE),
		CreatedAt: nostr.Timestamp(createdAt.Unix()),
		Tags:      tags,
		Content:   string(content),
	}, nil
}

// ValidateTaskStateEvent applies the NIP-CAS-0006 envelope, trust, content,
// mirror-tag, and claim rules.
func ValidateTaskStateEvent(event nostr.Event, policy TaskValidationPolicy) (TaskEventHead, error) {
	if event.Kind != nostr.Kind(events.CAS_CP_STATE) {
		return TaskEventHead{}, fmt.Errorf("unexpected task kind %d", event.Kind)
	}
	if !event.CheckID() || !event.VerifySignature() {
		return TaskEventHead{}, fmt.Errorf("invalid event id or signature")
	}
	pubkey := strings.ToLower(event.PubKey.Hex())
	if _, ok := policy.taskTrust()[pubkey]; !ok {
		return TaskEventHead{}, fmt.Errorf("untrusted task author %s", pubkey)
	}
	skew := policy.MaxFutureSkew
	if skew <= 0 {
		skew = 5 * time.Minute
	}
	if int64(event.CreatedAt) > policy.now().Add(skew).Unix() {
		return TaskEventHead{}, fmt.Errorf("task event is too far in the future")
	}
	dTag, err := requiredSingletonTag(event.Tags, "d")
	if err != nil || !strings.HasPrefix(dTag, "task:") || strings.TrimPrefix(dTag, "task:") == "" {
		return TaskEventHead{}, fmt.Errorf("invalid d tag")
	}
	if value, err := requiredSingletonTag(event.Tags, "domain"); err != nil || value != "task" {
		return TaskEventHead{}, fmt.Errorf("invalid domain tag")
	}
	if value, err := requiredSingletonTag(event.Tags, "schema"); err != nil || value != TaskStateSchemaV2 {
		return TaskEventHead{}, fmt.Errorf("invalid schema tag")
	}
	doc, err := ParseTaskDocument([]byte(event.Content))
	if err != nil {
		return TaskEventHead{}, fmt.Errorf("task content: %w", err)
	}
	if doc.ID != strings.TrimPrefix(dTag, "task:") {
		return TaskEventHead{}, fmt.Errorf("content id disagrees with d tag")
	}
	if value, err := requiredSingletonTag(event.Tags, "status"); err != nil || value != doc.Status {
		return TaskEventHead{}, fmt.Errorf("status tag disagrees with content")
	}
	if value, err := requiredSingletonTag(event.Tags, "priority"); err != nil || value != fmt.Sprintf("P%d", doc.Priority) {
		return TaskEventHead{}, fmt.Errorf("priority tag disagrees with content")
	}
	assignees := tagValues(event.Tags, "assignee")
	if doc.Assignee == "" {
		if len(assignees) != 0 {
			return TaskEventHead{}, fmt.Errorf("unexpected assignee tag")
		}
	} else if len(assignees) != 1 || assignees[0] != doc.Assignee {
		return TaskEventHead{}, fmt.Errorf("assignee tag disagrees with content")
	}
	if labels := tagValues(event.Tags, "t"); len(labels) > 0 && !sameStringSet(labels, doc.Labels) {
		return TaskEventHead{}, fmt.Errorf("label tags disagree with content")
	}
	if indexed := tagValues(event.Tags, "depends-on"); len(indexed) > 0 {
		ids := make([]string, 0, len(indexed))
		for _, value := range indexed {
			if !strings.HasPrefix(value, "task:") || strings.TrimPrefix(value, "task:") == "" {
				return TaskEventHead{}, fmt.Errorf("invalid depends-on tag")
			}
			ids = append(ids, strings.TrimPrefix(value, "task:"))
		}
		deps := make([]string, 0, len(doc.Dependencies))
		seen := map[string]struct{}{}
		for _, dep := range doc.Dependencies {
			if _, ok := seen[dep.DependsOnID]; !ok {
				seen[dep.DependsOnID] = struct{}{}
				deps = append(deps, dep.DependsOnID)
			}
		}
		if !sameStringSet(ids, deps) {
			return TaskEventHead{}, fmt.Errorf("dependency tags disagree with content")
		}
	}
	repoAttachments := 0
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		if tag[0] == "a" && len(tag) >= 4 && tag[3] == "nip34-repo" {
			repoAttachments++
			if repoAttachments > 1 || tag[1] != doc.Repository {
				return TaskEventHead{}, fmt.Errorf("repository tag disagrees with content")
			}
			continue
		}
		if tag[0] == "a" {
			addr := strings.SplitN(tag[1], ":", 4)
			if len(addr) == 4 && addr[0] == strconv.Itoa(TaskCollectionKind) {
				switch addr[2] {
				case "queue":
					if addr[3] == "" || addr[3] != doc.Queue {
						return TaskEventHead{}, fmt.Errorf("queue attachment disagrees with content")
					}
				case "epic":
					if addr[3] == "" || addr[3] != doc.Epic {
						return TaskEventHead{}, fmt.Errorf("epic attachment disagrees with content")
					}
				}
			}
		}
		if tag[0] == "e" && len(tag) >= 4 && tag[3] == "nip34-root" {
			if referenced := strings.TrimSpace(doc.Metadata["nostr.id"]); referenced != "" && referenced != tag[1] {
				return TaskEventHead{}, fmt.Errorf("NIP-34 root tag disagrees with metadata")
			}
			_, hasID := doc.Metadata["nostr.id"]
			_, hasKind := doc.Metadata["nostr.kind"]
			_, hasPubkey := doc.Metadata["nostr.pubkey"]
			if (hasID || hasKind || hasPubkey) && !(hasID && hasKind && hasPubkey) {
				return TaskEventHead{}, fmt.Errorf("NIP-34 root metadata identity must be complete")
			}
		}
	}
	claim, initial, err := taskClaimOrigin(event, doc, policy.taskTrust())
	if err != nil {
		return TaskEventHead{}, err
	}
	return TaskEventHead{Event: event, Task: doc, Claim: claim, InitialClaim: initial}, nil
}

func taskClaimOrigin(event nostr.Event, doc TaskDocument, trusted map[string]struct{}) (*ClaimOrigin, bool, error) {
	originID := strings.ToLower(strings.TrimSpace(doc.Metadata[ClaimOriginIDMetaKey]))
	originPubkey := strings.ToLower(strings.TrimSpace(doc.Metadata[ClaimOriginPubkeyMetaKey]))
	hasClaimField := doc.ClaimedAt != "" || originID != "" || originPubkey != ""
	if !hasClaimField {
		if doc.Status == "in_progress" {
			return nil, false, fmt.Errorf("in_progress task must carry a claim")
		}
		return nil, false, nil
	}
	if doc.Assignee == "" || doc.ClaimedAt == "" {
		return nil, false, fmt.Errorf("claim requires assignee and claimed_at")
	}
	if doc.Status == "open" {
		return nil, false, fmt.Errorf("open task cannot preserve a claim")
	}
	claimed, err := time.Parse(time.RFC3339Nano, doc.ClaimedAt)
	if err != nil {
		return nil, false, fmt.Errorf("invalid claimed_at")
	}
	claimedUnix := claimed.Unix()
	if (originID == "") != (originPubkey == "") {
		return nil, false, fmt.Errorf("claim origin metadata must be paired")
	}
	initial := originID == ""
	if initial {
		if doc.Status != "in_progress" || claimedUnix != int64(event.CreatedAt) {
			return nil, false, fmt.Errorf("initial claim must be in_progress and match event created_at")
		}
		originID = strings.ToLower(event.ID.Hex())
		originPubkey = strings.ToLower(event.PubKey.Hex())
	} else {
		if len(originID) != 64 || len(originPubkey) != 64 {
			return nil, false, fmt.Errorf("invalid claim origin metadata")
		}
		if _, err := hex.DecodeString(originID); err != nil {
			return nil, false, fmt.Errorf("invalid claim origin id")
		}
		if _, err := hex.DecodeString(originPubkey); err != nil {
			return nil, false, fmt.Errorf("invalid claim origin pubkey")
		}
		if _, ok := trusted[originPubkey]; !ok {
			return nil, false, fmt.Errorf("untrusted claim origin pubkey")
		}
		if originID == strings.ToLower(event.ID.Hex()) {
			return nil, false, fmt.Errorf("continuation cannot claim itself as origin")
		}
	}
	return &ClaimOrigin{EventID: originID, Pubkey: originPubkey, CreatedAt: claimedUnix, Assignee: doc.Assignee}, initial, nil
}

// TaskMerger retains latest valid per-author heads and computes effective tasks.
type TaskMerger struct {
	mu          sync.RWMutex
	policy      TaskValidationPolicy
	heads       map[string]map[string]TaskEventHead
	effective   map[string]TaskEventHead
	collections map[string]CollectionView
	origins     map[string]ClaimOrigin
}

func NewTaskMerger(policy TaskValidationPolicy) *TaskMerger {
	return &TaskMerger{
		policy:      policy,
		heads:       map[string]map[string]TaskEventHead{},
		effective:   map[string]TaskEventHead{},
		collections: map[string]CollectionView{},
		origins:     map[string]ClaimOrigin{},
	}
}

// IngestTask validates an event and returns the effective head and whether it changed.
func (m *TaskMerger) IngestTask(event nostr.Event) (TaskEventHead, bool, error) {
	head, err := ValidateTaskStateEvent(event, m.policy)
	if err != nil {
		return TaskEventHead{}, false, err
	}
	taskID := head.Task.ID
	author := strings.ToLower(event.PubKey.Hex())
	m.mu.Lock()
	defer m.mu.Unlock()
	if head.Claim != nil {
		if observed, ok := m.origins[head.Claim.EventID]; ok && !sameClaim(observed, *head.Claim) {
			return TaskEventHead{}, false, fmt.Errorf("claim lineage disagrees with observed origin")
		}
		if head.InitialClaim {
			m.origins[head.Claim.EventID] = *head.Claim
		}
	}
	if m.heads[taskID] == nil {
		m.heads[taskID] = map[string]TaskEventHead{}
	}
	if current, ok := m.heads[taskID][author]; ok && !eventWins(event, current.Event) {
		effective := m.effective[taskID]
		return cloneTaskHead(effective), false, nil
	}
	m.heads[taskID][author] = cloneTaskHead(head)
	next, ok := mergeTaskHeads(m.heads[taskID], m.origins)
	if !ok {
		return TaskEventHead{}, false, fmt.Errorf("no eligible task head")
	}
	prev, hadPrev := m.effective[taskID]
	changed := !hadPrev || prev.Event.ID != next.Event.ID
	m.effective[taskID] = cloneTaskHead(next)
	return cloneTaskHead(next), changed, nil
}

func mergeTaskHeads(byAuthor map[string]TaskEventHead, origins map[string]ClaimOrigin) (TaskEventHead, bool) {
	heads := make([]TaskEventHead, 0, len(byAuthor))
	var winning *ClaimOrigin
	for _, head := range byAuthor {
		if head.Claim != nil {
			if observed, ok := origins[head.Claim.EventID]; ok && !sameClaim(observed, *head.Claim) {
				continue
			}
		}
		heads = append(heads, head)
		if head.Claim != nil && (winning == nil || claimWins(*head.Claim, *winning)) {
			copyClaim := *head.Claim
			winning = &copyClaim
		}
	}
	var best TaskEventHead
	found := false
	for _, head := range heads {
		if winning != nil {
			if head.Claim == nil || !sameClaim(*head.Claim, *winning) {
				continue
			}
		}
		if !found || eventWins(head.Event, best.Event) {
			best = head
			found = true
		}
	}
	return cloneTaskHead(best), found
}

func claimWins(candidate, current ClaimOrigin) bool {
	if candidate.CreatedAt != current.CreatedAt {
		return candidate.CreatedAt < current.CreatedAt
	}
	return candidate.EventID < current.EventID
}

func sameClaim(a, b ClaimOrigin) bool {
	return a.EventID == b.EventID && a.Pubkey == b.Pubkey &&
		a.CreatedAt == b.CreatedAt && a.Assignee == b.Assignee
}

func eventWins(candidate, current nostr.Event) bool {
	if candidate.CreatedAt != current.CreatedAt {
		return candidate.CreatedAt > current.CreatedAt
	}
	return strings.ToLower(candidate.ID.Hex()) < strings.ToLower(current.ID.Hex())
}

// AuthorHead returns one trusted author's retained valid head.
func (m *TaskMerger) AuthorHead(taskID, pubkey string) (TaskEventHead, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	byAuthor := m.heads[strings.TrimSpace(taskID)]
	head, ok := byAuthor[strings.ToLower(strings.TrimSpace(pubkey))]
	return cloneTaskHead(head), ok
}

// EffectiveTask returns a defensive copy of the merged task.
func (m *TaskMerger) EffectiveTask(taskID string) (TaskEventHead, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	head, ok := m.effective[strings.TrimSpace(taskID)]
	return cloneTaskHead(head), ok
}

// EffectiveTasks returns defensive copies of every merged effective task,
// sorted by task ID.
func (m *TaskMerger) EffectiveTasks() []TaskEventHead {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TaskEventHead, 0, len(m.effective))
	for _, head := range m.effective {
		out = append(out, cloneTaskHead(head))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Task.ID < out[j].Task.ID })
	return out
}

// TaskHeads returns defensive copies of all retained per-author heads for one
// task, sorted by author pubkey. Empty when the task is unknown.
func (m *TaskMerger) TaskHeads(taskID string) []TaskEventHead {
	m.mu.RLock()
	defer m.mu.RUnlock()
	byAuthor := m.heads[strings.TrimSpace(taskID)]
	out := make([]TaskEventHead, 0, len(byAuthor))
	for _, head := range byAuthor {
		out = append(out, cloneTaskHead(head))
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Event.PubKey.Hex()) < strings.ToLower(out[j].Event.PubKey.Hex())
	})
	return out
}

// HasTask reports whether any trusted head is retained for the task ID, even
// when no effective merged state exists.
func (m *TaskMerger) HasTask(taskID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.heads[strings.TrimSpace(taskID)]) > 0
}

// TaskState returns the effective head and all retained author heads for one
// task under a single read lock, so callers see a consistent snapshot.
func (m *TaskMerger) TaskState(taskID string) (TaskEventHead, []TaskEventHead, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	taskID = strings.TrimSpace(taskID)
	effective, ok := m.effective[taskID]
	if !ok {
		return TaskEventHead{}, nil, false
	}
	byAuthor := m.heads[taskID]
	heads := make([]TaskEventHead, 0, len(byAuthor))
	for _, head := range byAuthor {
		heads = append(heads, cloneTaskHead(head))
	}
	sort.Slice(heads, func(i, j int) bool {
		return strings.ToLower(heads[i].Event.PubKey.Hex()) < strings.ToLower(heads[j].Event.PubKey.Hex())
	})
	return cloneTaskHead(effective), heads, true
}

func cloneTaskHead(head TaskEventHead) TaskEventHead {
	out := head
	if head.Claim != nil {
		claim := *head.Claim
		out.Claim = &claim
	}
	if len(head.Event.Tags) > 0 {
		tags := make(nostr.Tags, len(head.Event.Tags))
		for i, tag := range head.Event.Tags {
			tags[i] = append(nostr.Tag(nil), tag...)
		}
		out.Event.Tags = tags
	}
	raw, err := EncodeTaskDocument(head.Task)
	if err == nil {
		if parsed, parseErr := ParseTaskDocument(raw); parseErr == nil {
			out.Task = parsed
		}
	}
	return out
}

// CollectionView is one list author's independently replaceable queue/epic view.
type CollectionView struct {
	Type      string      `json:"type"`
	ID        string      `json:"id"`
	Author    string      `json:"author"`
	EventID   string      `json:"event_id"`
	CreatedAt int64       `json:"created_at"`
	TaskIDs   []string    `json:"task_ids"`
	Event     nostr.Event `json:"-"`
}

// BuildTaskCollectionEvent builds an unsigned NIP-51 queue or epic list.
func BuildTaskCollectionEvent(collectionType, id string, members []TaskEventHead, createdAt time.Time) (nostr.Event, error) {
	collectionType = strings.ToLower(strings.TrimSpace(collectionType))
	id = strings.TrimSpace(id)
	if (collectionType != "queue" && collectionType != "epic") || id == "" {
		return nostr.Event{}, fmt.Errorf("collection type must be queue or epic and id is required")
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	tags := nostr.Tags{{"d", collectionType + ":" + id}}
	sort.Slice(members, func(i, j int) bool { return members[i].Task.ID < members[j].Task.ID })
	seen := map[string]struct{}{}
	for _, member := range members {
		if _, ok := seen[member.Task.ID]; ok {
			continue
		}
		seen[member.Task.ID] = struct{}{}
		expected := member.Task.Queue
		if collectionType == "epic" {
			expected = member.Task.Epic
		}
		if expected != id {
			return nostr.Event{}, fmt.Errorf("task %s does not belong to %s:%s", member.Task.ID, collectionType, id)
		}
		addr := strconv.Itoa(int(events.CAS_CP_STATE)) + ":" + member.Event.PubKey.Hex() + ":task:" + member.Task.ID
		tags = append(tags, nostr.Tag{"a", addr, ""})
	}
	return nostr.Event{Kind: nostr.Kind(TaskCollectionKind), CreatedAt: nostr.Timestamp(createdAt.Unix()), Tags: tags, Content: ""}, nil
}

// IngestCollection validates and retains one full author coordinate.
func (m *TaskMerger) IngestCollection(event nostr.Event) (CollectionView, bool, error) {
	view, err := validateTaskCollection(event, m.policy)
	if err != nil {
		return CollectionView{}, false, err
	}
	key := view.Author + "|" + view.Type + ":" + view.ID
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.collections[key]; ok && !eventWins(event, current.Event) {
		return cloneCollection(current), false, nil
	}
	m.collections[key] = cloneCollection(view)
	return cloneCollection(view), true, nil
}

func validateTaskCollection(event nostr.Event, policy TaskValidationPolicy) (CollectionView, error) {
	if event.Kind != nostr.Kind(TaskCollectionKind) {
		return CollectionView{}, fmt.Errorf("unexpected collection kind %d", event.Kind)
	}
	if !event.CheckID() || !event.VerifySignature() {
		return CollectionView{}, fmt.Errorf("invalid event id or signature")
	}
	author := strings.ToLower(event.PubKey.Hex())
	if _, ok := policy.collectionTrust()[author]; !ok {
		return CollectionView{}, fmt.Errorf("untrusted collection author %s", author)
	}
	skew := policy.MaxFutureSkew
	if skew <= 0 {
		skew = 5 * time.Minute
	}
	if int64(event.CreatedAt) > policy.now().Add(skew).Unix() {
		return CollectionView{}, fmt.Errorf("collection event is too far in the future")
	}
	dTag, err := requiredSingletonTag(event.Tags, "d")
	if err != nil {
		return CollectionView{}, err
	}
	parts := strings.SplitN(dTag, ":", 2)
	if len(parts) != 2 || (parts[0] != "queue" && parts[0] != "epic") || parts[1] == "" {
		return CollectionView{}, fmt.Errorf("collection d must be queue:<id> or epic:<id>")
	}
	taskTrust := policy.taskTrust()
	seen := map[string]struct{}{}
	var taskIDs []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "a" {
			continue
		}
		addr := strings.SplitN(tag[1], ":", 4)
		if len(addr) != 4 || addr[0] != strconv.Itoa(int(events.CAS_CP_STATE)) || addr[2] != "task" || addr[3] == "" {
			continue
		}
		if _, ok := taskTrust[strings.ToLower(addr[1])]; !ok {
			continue
		}
		if _, ok := seen[addr[3]]; ok {
			continue
		}
		seen[addr[3]] = struct{}{}
		taskIDs = append(taskIDs, addr[3])
	}
	sort.Strings(taskIDs)
	return CollectionView{Type: parts[0], ID: parts[1], Author: author, EventID: event.ID.Hex(), CreatedAt: int64(event.CreatedAt), TaskIDs: taskIDs, Event: event}, nil
}

// Collections returns independent author views, optionally filtered by type/id.
func (m *TaskMerger) Collections(collectionType, id string) []CollectionView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []CollectionView
	for _, view := range m.collections {
		if collectionType != "" && view.Type != collectionType {
			continue
		}
		if id != "" && view.ID != id {
			continue
		}
		out = append(out, cloneCollection(view))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Author < out[j].Author
	})
	return out
}

// CollectionMembers resolves logical pointers against current effective tasks.
func (m *TaskMerger) CollectionMembers(view CollectionView) []TaskEventHead {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []TaskEventHead
	for _, taskID := range view.TaskIDs {
		head, ok := m.effective[taskID]
		if !ok {
			continue
		}
		expected := head.Task.Queue
		if view.Type == "epic" {
			expected = head.Task.Epic
		}
		if expected == view.ID {
			out = append(out, cloneTaskHead(head))
		}
	}
	return out
}

func cloneCollection(view CollectionView) CollectionView {
	view.TaskIDs = append([]string(nil), view.TaskIDs...)
	return view
}

func requiredSingletonTag(tags nostr.Tags, name string) (string, error) {
	values := tagValues(tags, name)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", fmt.Errorf("tag %s must appear exactly once", name)
	}
	return values[0], nil
}

func tagValues(tags nostr.Tags, name string) []string {
	var values []string
	for _, tag := range tags {
		if len(tag) >= 2 && tag[0] == name {
			values = append(values, tag[1])
		}
	}
	return values
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	return bytes.Equal([]byte(strings.Join(left, "\x00")), []byte(strings.Join(right, "\x00")))
}

func normalizedPubkeySet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}
