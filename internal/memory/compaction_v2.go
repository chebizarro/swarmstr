package memory

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

type duplicateCluster struct {
	KeepID  string
	Members []MemoryRecord
}

type supersessionHealth struct {
	MissingTargets []string
	Cycles         []string
	Conflicts      []string
	Repairs        int
}

func loadAllMemoryRecords(db *sql.DB) ([]MemoryRecord, error) {
	rows, err := db.Query(memoryRecordSelectSQL("0.0") + `
		FROM memory_records r
		ORDER BY r.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	b := &SQLiteBackend{}
	recs, _ := b.scanMemoryRecordRows(rows)
	return recs, nil
}

func detectDuplicateClusters(records []MemoryRecord) []duplicateCluster {
	groups := map[string][]MemoryRecord{}
	for _, rec := range records {
		if rec.DeletedAt != nil || rec.SupersededBy != "" {
			continue
		}
		norm := normalizeComparableText(rec.Text)
		if norm == "" {
			continue
		}
		groups[norm] = append(groups[norm], rec)
	}
	clusters := make([]duplicateCluster, 0)
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		keep := chooseClusterKeeper(members)
		clusters = append(clusters, duplicateCluster{KeepID: keep.ID, Members: members})
	}
	return clusters
}

func chooseClusterKeeper(records []MemoryRecord) MemoryRecord {
	sorted := append([]MemoryRecord(nil), records...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a := sorted[i]
		b := sorted[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if isDurableRecord(a) != isDurableRecord(b) {
			return isDurableRecord(a)
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.After(b.UpdatedAt)
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.ID < b.ID
	})
	return sorted[0]
}

func isDurableRecord(rec MemoryRecord) bool {
	if rec.Pinned || strings.TrimSpace(rec.Source.FilePath) != "" {
		return true
	}
	if rec.Metadata == nil {
		return false
	}
	v, ok := rec.Metadata["durable"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func normalizeComparableText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func applyStaleFlag(db *sql.DB, rec MemoryRecord, now time.Time, reason string) bool {
	if reason == "" || rec.Pinned || isDurableRecord(rec) {
		return false
	}
	meta := cloneMetadata(rec.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	if v, ok := meta["stale"].(bool); ok && v {
		if s, ok := meta["stale_reason"].(string); ok && s == reason {
			return false
		}
	}
	meta["stale"] = true
	meta["stale_reason"] = reason
	_, err := db.Exec(`UPDATE memory_records SET metadata = ?, updated_at = ? WHERE id = ?`, recordJSON(meta), now.Unix(), rec.ID)
	return err == nil
}

func staleReason(rec MemoryRecord, now time.Time, episodeCutoff time.Time) string {
	if rec.DeletedAt != nil || rec.SupersededBy != "" {
		return ""
	}
	if rec.ValidUntil != nil && !rec.ValidUntil.After(now) {
		return "expired"
	}
	if rec.Type == MemoryRecordTypeEpisode && rec.UpdatedAt.Before(episodeCutoff) {
		return "stale_episode"
	}
	return ""
}

func pinnedDurableSupersessionTargets(records []MemoryRecord) map[string]bool {
	out := map[string]bool{}
	for _, rec := range records {
		if !rec.Pinned && !isDurableRecord(rec) {
			continue
		}
		for _, id := range rec.Supersedes {
			id = strings.TrimSpace(id)
			if id != "" {
				out[id] = true
			}
		}
	}
	return out
}

func repairSupersessionCycles(db *sql.DB, records []MemoryRecord, now time.Time) (int, []string) {
	byID := make(map[string]MemoryRecord, len(records))
	for _, rec := range records {
		byID[rec.ID] = rec
	}
	visited := map[string]bool{}
	repaired := map[string]bool{}
	fixes := 0
	warnings := []string{}
	for _, start := range records {
		if visited[start.ID] || start.SupersededBy == "" {
			continue
		}
		pathIndex := map[string]int{}
		path := []MemoryRecord{}
		curID := start.ID
		for strings.TrimSpace(curID) != "" {
			if idx, ok := pathIndex[curID]; ok {
				cycle := path[idx:]
				if len(cycle) > 0 && !cycleAlreadyRepaired(cycle, repaired) {
					n, ws := repairSupersessionCycle(db, cycle, now)
					fixes += n
					warnings = append(warnings, ws...)
					for _, rec := range cycle {
						repaired[rec.ID] = true
					}
				}
				break
			}
			if visited[curID] {
				break
			}
			rec, ok := byID[curID]
			if !ok {
				break
			}
			pathIndex[curID] = len(path)
			path = append(path, rec)
			curID = rec.SupersededBy
		}
		for _, rec := range path {
			visited[rec.ID] = true
		}
	}
	return fixes, warnings
}

func cycleAlreadyRepaired(cycle []MemoryRecord, repaired map[string]bool) bool {
	for _, rec := range cycle {
		if repaired[rec.ID] {
			return true
		}
	}
	return false
}

func repairSupersessionCycle(db *sql.DB, cycle []MemoryRecord, now time.Time) (int, []string) {
	keeper := chooseClusterKeeper(cycle)
	fixes := 0
	warnings := []string{}
	for _, rec := range cycle {
		next := ""
		if rec.ID != keeper.ID && !rec.Pinned && !isDurableRecord(rec) {
			next = keeper.ID
		}
		res, err := db.Exec(`UPDATE memory_records SET superseded_by = ?, updated_at = ? WHERE id = ?`, next, now.Unix(), rec.ID)
		if err != nil {
			warnings = append(warnings, "repair supersession cycle "+rec.ID+": "+err.Error())
			continue
		}
		if n, nerr := res.RowsAffected(); nerr == nil && n > 0 {
			fixes += int(n)
		}
	}
	return fixes, warnings
}

func decodeSupersedes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return normalizeStringSlice(out)
}
