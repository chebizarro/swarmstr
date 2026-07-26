package skills

// proposals.go — Skill-workshop proposal store (skills.proposals.* gateway
// methods).
//
// Proposals are file-backed under <workspace>/.metiq/skill-proposals/<id>/:
//
//	proposal.json   the ProposalRecord metadata
//	PROPOSAL.md     the staged SKILL.md draft body
//	support/<path>  staged support files (verbatim)
//
// create/update stage a draft (+optional support files) and run the metiq skill
// linter as a static "scan". revise replaces the draft on a pending proposal and
// re-scans. apply writes the draft SKILL.md + support files into the target skill
// directory only when the scan is clean. reject/quarantine set a terminal status.
//
// Record shape mirrors OpenClaw's skill-workshop proposal schema, adapted to
// metiq (no ClawHub; the scan is the local lint verdict, not a remote check).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metiq/internal/store/state"
)

const (
	// ProposalSchema identifies the proposal record schema (metiq adaptation of
	// the OpenClaw skill-workshop proposal contract).
	ProposalSchema = "openclaw.skill-workshop.proposal.v1"
	// proposalsDirName is the proposal store directory under the workspace .metiq dir.
	proposalsDirName = "skill-proposals"
	// proposalRecordFile is the per-proposal metadata file.
	proposalRecordFile = "proposal.json"
	// proposalDraftFile is the per-proposal staged SKILL.md draft.
	proposalDraftFile = "PROPOSAL.md"
	// proposalSupportDir is the per-proposal support-file subdirectory.
	proposalSupportDir = "support"

	// MaxProposalDraftBytes bounds a staged draft body.
	MaxProposalDraftBytes = 1 * 1024 * 1024
	// MaxProposalSupportFiles bounds the number of staged support files.
	MaxProposalSupportFiles = 64
	// MaxProposalSupportFileBytes bounds one staged support file.
	MaxProposalSupportFileBytes = 256 * 1024
)

// ProposalScanCounts summarizes lint findings by severity.
type ProposalScanCounts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
}

// ProposalScan is the static scan verdict for a staged draft.
type ProposalScan struct {
	State    string             `json:"state"` // pending|clean|failed|quarantined
	Counts   ProposalScanCounts `json:"counts"`
	Findings []LintFinding      `json:"findings"`
}

// ProposalSupportFile is support-file metadata recorded on a proposal.
type ProposalSupportFile struct {
	Path string `json:"path"`
	Hash string `json:"hash,omitempty"`
}

// ProposalTarget describes the skill a proposal writes to.
type ProposalTarget struct {
	SkillName          string `json:"skillName"`
	SkillKey           string `json:"skillKey"`
	SkillDir           string `json:"skillDir"`
	SkillFile          string `json:"skillFile"`
	CurrentContentHash string `json:"currentContentHash"`
}

// ProposalRecord is one skill-workshop proposal.
type ProposalRecord struct {
	Schema          string                `json:"schema"`
	ID              string                `json:"id"`
	Kind            string                `json:"kind"`   // create|update
	Status          string                `json:"status"` // pending|applied|rejected|quarantined|stale
	Title           string                `json:"title"`
	Description     string                `json:"description"`
	CreatedAt       string                `json:"createdAt"`
	UpdatedAt       string                `json:"updatedAt"`
	CreatedBy       string                `json:"createdBy"`
	ProposedVersion string                `json:"proposedVersion,omitempty"`
	DraftFile       string                `json:"draftFile"`
	DraftHash       string                `json:"draftHash"`
	SupportFiles    []ProposalSupportFile `json:"supportFiles"`
	Target          ProposalTarget        `json:"target"`
	Scan            ProposalScan          `json:"scan"`
	Reason          string                `json:"reason,omitempty"`
}

// ProposalFileInput is one staged support file (verbatim content).
type ProposalFileInput struct {
	Path    string
	Content string
}

// ProposalDraftInput carries the mutable fields for create/update/revise.
type ProposalDraftInput struct {
	Title           string
	Description     string
	Content         string
	ProposedVersion string
	SkillName       string
	SkillKey        string
	SupportFiles    []ProposalFileInput
}

// ProposalStore is a file-backed skill-proposal store for one agent workspace.
type ProposalStore struct {
	root         string
	workspaceDir string
	cfg          state.ConfigDoc
	agentID      string
}

// NewProposalStore returns a proposal store rooted at the agent workspace.
func NewProposalStore(cfg state.ConfigDoc, agentID string) *ProposalStore {
	workspaceDir := ResolveAgentWorkspaceDir(cfg, agentID)
	return &ProposalStore{
		root:         filepath.Join(workspaceDir, ".metiq", proposalsDirName),
		workspaceDir: workspaceDir,
		cfg:          cfg,
		agentID:      agentID,
	}
}

func rfc3339Now() string { return time.Now().UTC().Format(time.RFC3339) }

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newProposalID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "prop_" + hex.EncodeToString(buf), nil
}

func (s *ProposalStore) proposalDir(id string) string { return filepath.Join(s.root, id) }

func (s *ProposalStore) recordPath(id string) string {
	return filepath.Join(s.proposalDir(id), proposalRecordFile)
}

func (s *ProposalStore) draftPath(id string) string {
	return filepath.Join(s.proposalDir(id), proposalDraftFile)
}

// sanitizeSupportPath rejects absolute paths and traversal escapes, returning a
// clean workspace-relative slash path.
func sanitizeSupportPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("support file path is required")
	}
	p = filepath.ToSlash(p)
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("support file path must be relative: %q", raw)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("support file path escapes proposal: %q", raw)
	}
	if strings.EqualFold(clean, proposalDraftFile) || strings.EqualFold(clean, proposalRecordFile) {
		return "", fmt.Errorf("support file path is reserved: %q", raw)
	}
	return clean, nil
}

func validateDraftInput(in ProposalDraftInput) ([]ProposalFileInput, error) {
	if len(in.Content) > MaxProposalDraftBytes {
		return nil, fmt.Errorf("draft content exceeds %d bytes", MaxProposalDraftBytes)
	}
	if len(in.SupportFiles) > MaxProposalSupportFiles {
		return nil, fmt.Errorf("too many support files (max %d)", MaxProposalSupportFiles)
	}
	seen := map[string]struct{}{}
	out := make([]ProposalFileInput, 0, len(in.SupportFiles))
	for _, f := range in.SupportFiles {
		clean, err := sanitizeSupportPath(f.Path)
		if err != nil {
			return nil, err
		}
		if len(f.Content) > MaxProposalSupportFileBytes {
			return nil, fmt.Errorf("support file %q exceeds %d bytes", clean, MaxProposalSupportFileBytes)
		}
		lower := strings.ToLower(clean)
		if _, ok := seen[lower]; ok {
			return nil, fmt.Errorf("duplicate support file path: %q", clean)
		}
		seen[lower] = struct{}{}
		out = append(out, ProposalFileInput{Path: clean, Content: f.Content})
	}
	return out, nil
}

// resolveTarget resolves the target skill for a proposal of the given kind.
func (s *ProposalStore) resolveTarget(kind string, in ProposalDraftInput) (ProposalTarget, error) {
	name := strings.TrimSpace(in.SkillName)
	key := normalizedSkillKey(strings.TrimSpace(in.SkillKey))
	if key == "" {
		key = normalizedSkillKey(name)
	}
	if key == "" {
		return ProposalTarget{}, fmt.Errorf("skillName or skillKey is required")
	}

	catalog, err := BuildSkillCatalog(s.cfg, s.agentID)
	if err != nil {
		return ProposalTarget{}, err
	}
	index, _ := catalogSkillIndex(catalog)
	resolved, exists := index[key]

	if kind == "update" {
		if !exists {
			return ProposalTarget{}, fmt.Errorf("skill %q not found for update proposal", key)
		}
		skillFile := strings.TrimSpace(resolved.Skill.FilePath)
		skillDir := strings.TrimSpace(resolved.Skill.BaseDir)
		if skillDir == "" && skillFile != "" {
			skillDir = filepath.Dir(skillFile)
		}
		return ProposalTarget{
			SkillName:          curatorSkillName(resolved),
			SkillKey:           key,
			SkillDir:           skillDir,
			SkillFile:          skillFile,
			CurrentContentHash: hashFileIfPresent(skillFile),
		}, nil
	}

	// kind == "create": target a new workspace skill directory.
	if name == "" {
		if resolved != nil && resolved.Skill != nil {
			name = curatorSkillName(resolved)
		} else {
			name = key
		}
	}
	skillDir := filepath.Join(s.workspaceDir, key)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if exists {
		// A create proposal against an existing key still records its current hash.
		if fp := strings.TrimSpace(resolved.Skill.FilePath); fp != "" {
			skillFile = fp
			skillDir = strings.TrimSpace(resolved.Skill.BaseDir)
			if skillDir == "" {
				skillDir = filepath.Dir(skillFile)
			}
		}
	}
	return ProposalTarget{
		SkillName:          name,
		SkillKey:           key,
		SkillDir:           skillDir,
		SkillFile:          skillFile,
		CurrentContentHash: hashFileIfPresent(skillFile),
	}, nil
}

func hashFileIfPresent(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return sha256Hex(raw)
}

// scanDraft writes the draft (+support files) to a temp skill directory and runs
// the metiq skill linter, returning the scan verdict.
func scanDraft(skillKey, content string, supportFiles []ProposalFileInput) (ProposalScan, error) {
	key := normalizedSkillKey(skillKey)
	if key == "" {
		key = "proposal"
	}
	tmpRoot, err := os.MkdirTemp("", "metiq-skill-scan-")
	if err != nil {
		return ProposalScan{}, err
	}
	defer os.RemoveAll(tmpRoot)

	skillDir := filepath.Join(tmpRoot, key)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return ProposalScan{}, err
	}
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		return ProposalScan{}, err
	}
	for _, f := range supportFiles {
		dest := filepath.Join(skillDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return ProposalScan{}, err
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return ProposalScan{}, err
		}
	}

	report, lintErr := LintSkillFile(skillFile)
	if lintErr != nil {
		report = LintReport{Findings: []LintFinding{{Severity: LintError, Code: "parse-error", Message: lintErr.Error()}}}
	}
	findings := report.Findings
	if findings == nil {
		findings = []LintFinding{}
	}
	counts := ProposalScanCounts{}
	for _, f := range findings {
		switch f.Severity {
		case LintError:
			counts.Error++
		case LintWarning:
			counts.Warning++
		}
	}
	scanState := "clean"
	if counts.Error > 0 {
		scanState = "failed"
	}
	return ProposalScan{State: scanState, Counts: counts, Findings: findings}, nil
}

func (s *ProposalStore) writeRecord(rec ProposalRecord) error {
	if err := os.MkdirAll(s.proposalDir(rec.ID), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(s.recordPath(rec.ID), raw, 0o644)
}

// writeDraftFiles (re)writes the draft body and support files for a proposal,
// clearing any previously staged support files.
func (s *ProposalStore) writeDraftFiles(id, content string, supportFiles []ProposalFileInput) ([]ProposalSupportFile, error) {
	dir := s.proposalDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.draftPath(id), []byte(content), 0o644); err != nil {
		return nil, err
	}
	supportRoot := filepath.Join(dir, proposalSupportDir)
	_ = os.RemoveAll(supportRoot)
	meta := make([]ProposalSupportFile, 0, len(supportFiles))
	for _, f := range supportFiles {
		dest := filepath.Join(supportRoot, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return nil, err
		}
		meta = append(meta, ProposalSupportFile{Path: f.Path, Hash: sha256Hex([]byte(f.Content))})
	}
	return meta, nil
}

func (s *ProposalStore) loadRecord(id string) (ProposalRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ProposalRecord{}, fmt.Errorf("proposalId is required")
	}
	raw, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return ProposalRecord{}, state.ErrNotFound
		}
		return ProposalRecord{}, err
	}
	var rec ProposalRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return ProposalRecord{}, err
	}
	if rec.SupportFiles == nil {
		rec.SupportFiles = []ProposalSupportFile{}
	}
	if rec.Scan.Findings == nil {
		rec.Scan.Findings = []LintFinding{}
	}
	return rec, nil
}

// Load returns the proposal record for id (metadata only; no draft/support
// bodies). Returns state.ErrNotFound when the proposal does not exist.
func (s *ProposalStore) Load(id string) (ProposalRecord, error) {
	return s.loadRecord(id)
}

// DraftContent returns the staged SKILL.md draft body for a proposal.
func (s *ProposalStore) DraftContent(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("proposalId is required")
	}
	raw, err := os.ReadFile(s.draftPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return "", state.ErrNotFound
		}
		return "", err
	}
	return string(raw), nil
}

// create stages a new proposal of the given kind.
func (s *ProposalStore) create(kind string, in ProposalDraftInput) (ProposalRecord, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return ProposalRecord{}, fmt.Errorf("title is required")
	}
	supportFiles, err := validateDraftInput(in)
	if err != nil {
		return ProposalRecord{}, err
	}
	target, err := s.resolveTarget(kind, in)
	if err != nil {
		return ProposalRecord{}, err
	}
	scan, err := scanDraft(target.SkillKey, in.Content, supportFiles)
	if err != nil {
		return ProposalRecord{}, err
	}
	id, err := newProposalID()
	if err != nil {
		return ProposalRecord{}, err
	}
	meta, err := s.writeDraftFiles(id, in.Content, supportFiles)
	if err != nil {
		return ProposalRecord{}, err
	}
	now := rfc3339Now()
	rec := ProposalRecord{
		Schema:          ProposalSchema,
		ID:              id,
		Kind:            kind,
		Status:          "pending",
		Title:           title,
		Description:     strings.TrimSpace(in.Description),
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       "gateway",
		ProposedVersion: strings.TrimSpace(in.ProposedVersion),
		DraftFile:       proposalDraftFile,
		DraftHash:       sha256Hex([]byte(in.Content)),
		SupportFiles:    meta,
		Target:          target,
		Scan:            scan,
	}
	if err := s.writeRecord(rec); err != nil {
		return ProposalRecord{}, err
	}
	return rec, nil
}

// Create stages a proposal for a NEW skill (kind=create).
func (s *ProposalStore) Create(in ProposalDraftInput) (ProposalRecord, error) {
	return s.create("create", in)
}

// Update stages a proposal that modifies an EXISTING skill (kind=update).
func (s *ProposalStore) Update(in ProposalDraftInput) (ProposalRecord, error) {
	return s.create("update", in)
}

// Revise replaces the draft body/support files on a pending proposal and rescans.
func (s *ProposalStore) Revise(id string, in ProposalDraftInput) (ProposalRecord, error) {
	rec, err := s.loadRecord(id)
	if err != nil {
		return ProposalRecord{}, err
	}
	if rec.Status != "pending" {
		return ProposalRecord{}, fmt.Errorf("proposal %q is %s; only pending proposals can be revised", rec.ID, rec.Status)
	}
	supportFiles, err := validateDraftInput(in)
	if err != nil {
		return ProposalRecord{}, err
	}
	scan, err := scanDraft(rec.Target.SkillKey, in.Content, supportFiles)
	if err != nil {
		return ProposalRecord{}, err
	}
	meta, err := s.writeDraftFiles(rec.ID, in.Content, supportFiles)
	if err != nil {
		return ProposalRecord{}, err
	}
	if t := strings.TrimSpace(in.Title); t != "" {
		rec.Title = t
	}
	if d := strings.TrimSpace(in.Description); d != "" {
		rec.Description = d
	}
	if v := strings.TrimSpace(in.ProposedVersion); v != "" {
		rec.ProposedVersion = v
	}
	rec.DraftHash = sha256Hex([]byte(in.Content))
	rec.SupportFiles = meta
	rec.Scan = scan
	rec.UpdatedAt = rfc3339Now()
	if err := s.writeRecord(rec); err != nil {
		return ProposalRecord{}, err
	}
	return rec, nil
}

// Apply writes the staged draft into the target skill directory when the scan is
// clean, marking the proposal applied.
func (s *ProposalStore) Apply(id string) (map[string]any, error) {
	rec, err := s.loadRecord(id)
	if err != nil {
		return nil, err
	}
	if rec.Status != "pending" {
		return nil, fmt.Errorf("proposal %q is %s; only pending proposals can be applied", rec.ID, rec.Status)
	}
	if rec.Scan.State != "clean" {
		return nil, fmt.Errorf("proposal %q scan is %q; only clean proposals can be applied", rec.ID, rec.Scan.State)
	}
	skillFile := strings.TrimSpace(rec.Target.SkillFile)
	if skillFile == "" {
		return nil, fmt.Errorf("proposal %q has no target skill file", rec.ID)
	}
	skillDir := strings.TrimSpace(rec.Target.SkillDir)
	if skillDir == "" {
		skillDir = filepath.Dir(skillFile)
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return nil, err
	}
	draft, err := os.ReadFile(s.draftPath(rec.ID))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(skillFile, draft, 0o644); err != nil {
		return nil, err
	}
	// Copy staged support files verbatim into the skill directory.
	for _, f := range rec.SupportFiles {
		src := filepath.Join(s.proposalDir(rec.ID), proposalSupportDir, filepath.FromSlash(f.Path))
		content, readErr := os.ReadFile(src)
		if readErr != nil {
			return nil, readErr
		}
		dest := filepath.Join(skillDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return nil, err
		}
	}
	rec.Status = "applied"
	rec.UpdatedAt = rfc3339Now()
	if err := s.writeRecord(rec); err != nil {
		return nil, err
	}
	InvalidateSkillCatalogCache()
	return map[string]any{"record": rec, "targetSkillFile": skillFile}, nil
}

// setTerminalStatus records a terminal status + reason on a pending proposal.
func (s *ProposalStore) setTerminalStatus(id, status, reason string) (ProposalRecord, error) {
	rec, err := s.loadRecord(id)
	if err != nil {
		return ProposalRecord{}, err
	}
	if rec.Status == "applied" {
		return ProposalRecord{}, fmt.Errorf("proposal %q is already applied", rec.ID)
	}
	rec.Status = status
	rec.Reason = strings.TrimSpace(reason)
	if status == "quarantined" {
		rec.Scan.State = "quarantined"
	}
	rec.UpdatedAt = rfc3339Now()
	if err := s.writeRecord(rec); err != nil {
		return ProposalRecord{}, err
	}
	return rec, nil
}

// Reject marks a proposal rejected with an optional reason.
func (s *ProposalStore) Reject(id, reason string) (ProposalRecord, error) {
	return s.setTerminalStatus(id, "rejected", reason)
}

// Quarantine marks a proposal quarantined with an optional reason.
func (s *ProposalStore) Quarantine(id, reason string) (ProposalRecord, error) {
	return s.setTerminalStatus(id, "quarantined", reason)
}

// List returns the proposal manifest over all stored proposals.
func (s *ProposalStore) List() (map[string]any, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"schema": ProposalSchema, "updatedAt": "", "proposals": []ProposalRecord{}}, nil
		}
		return nil, err
	}
	records := make([]ProposalRecord, 0, len(entries))
	updatedAt := ""
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rec, loadErr := s.loadRecord(entry.Name())
		if loadErr != nil {
			continue
		}
		records = append(records, rec)
		if rec.UpdatedAt > updatedAt {
			updatedAt = rec.UpdatedAt
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt != records[j].CreatedAt {
			return records[i].CreatedAt > records[j].CreatedAt
		}
		return records[i].ID < records[j].ID
	})
	return map[string]any{
		"schema":    ProposalSchema,
		"updatedAt": updatedAt,
		"proposals": records,
	}, nil
}

// Inspect returns a proposal record plus its draft body and support-file content.
func (s *ProposalStore) Inspect(id string) (map[string]any, error) {
	rec, err := s.loadRecord(id)
	if err != nil {
		return nil, err
	}
	content := ""
	if raw, readErr := os.ReadFile(s.draftPath(rec.ID)); readErr == nil {
		content = string(raw)
	}
	supportFiles := make([]map[string]any, 0, len(rec.SupportFiles))
	for _, f := range rec.SupportFiles {
		src := filepath.Join(s.proposalDir(rec.ID), proposalSupportDir, filepath.FromSlash(f.Path))
		fileContent := ""
		if raw, readErr := os.ReadFile(src); readErr == nil {
			fileContent = string(raw)
		}
		supportFiles = append(supportFiles, map[string]any{
			"path":    f.Path,
			"content": fileContent,
		})
	}
	return map[string]any{
		"record":       rec,
		"content":      content,
		"supportFiles": supportFiles,
	}, nil
}
