// Package cron provides a minimal cron-expression parser and scheduler helper.
//
// Supported syntax:
//   - 5-field expressions: <min> <hour> <dom> <mon> <dow>
//   - Field values: * | number | list (1,3,5) | range (1-5) | step (*/5, 1-30/5)
//   - Shorthands: @hourly, @daily, @midnight, @weekly, @monthly, @annually, @yearly
//   - @every <duration>  — e.g. "@every 5m", "@every 1h30m"
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule represents a parsed cron expression.
type Schedule interface {
	// Matches reports whether the given time satisfies this schedule.
	// Resolution is minute-level: seconds are ignored.
	Matches(t time.Time) bool
	// Next returns the next time at or after t that satisfies the schedule.
	Next(t time.Time) time.Time
}

// Parse parses a cron expression or shorthand and returns a Schedule.
func Parse(expr string) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "@every") {
		rest := strings.TrimSpace(strings.TrimPrefix(expr, "@every"))
		d, err := time.ParseDuration(rest)
		if err != nil {
			return nil, fmt.Errorf("invalid @every duration %q: %w", rest, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("@every duration must be positive")
		}
		return &intervalSchedule{interval: d, origin: time.Time{}}, nil
	}

	switch expr {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	case "@annually", "@yearly":
		expr = "0 0 1 1 *"
	}

	return parseFields(expr)
}

// ─── 5-field parser ──────────────────────────────────────────────────────────

type fieldSchedule struct {
	minute  []bool // indexed 0–59
	hour    []bool // indexed 0–23
	dom     []bool // indexed 1–31
	month   []bool // indexed 1–12
	dow     []bool // indexed 0–6 (0=Sunday)
}

func parseFields(expr string) (*fieldSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d: %q", len(fields), expr)
	}
	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field %q: %w", fields[0], err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field %q: %w", fields[1], err)
	}
	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-month field %q: %w", fields[2], err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("invalid month field %q: %w", fields[3], err)
	}
	dow, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("invalid day-of-week field %q: %w", fields[4], err)
	}
	return &fieldSchedule{minute: minute, hour: hour, dom: dom, month: month, dow: dow}, nil
}

func parseField(s string, lo, hi int) ([]bool, error) {
	set := make([]bool, hi+1)
	for _, part := range strings.Split(s, ",") {
		if err := applyPart(part, lo, hi, set); err != nil {
			return nil, err
		}
	}
	return set, nil
}

func applyPart(s string, lo, hi int, set []bool) error {
	// Step: "*/n" or "a-b/n"
	step := 1
	if idx := strings.Index(s, "/"); idx >= 0 {
		var err error
		step, err = strconv.Atoi(s[idx+1:])
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step value in %q", s)
		}
		s = s[:idx]
	}

	var start, end int
	if s == "*" {
		start, end = lo, hi
	} else if idx := strings.Index(s, "-"); idx >= 0 {
		a, err1 := strconv.Atoi(s[:idx])
		b, err2 := strconv.Atoi(s[idx+1:])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("invalid range in %q", s)
		}
		start, end = a, b
	} else {
		v, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid value %q", s)
		}
		start, end = v, v
	}

	if start < lo || end > hi || start > end {
		return fmt.Errorf("value %d–%d out of range %d–%d", start, end, lo, hi)
	}
	for i := start; i <= end; i += step {
		set[i] = true
	}
	return nil
}

func (s *fieldSchedule) Matches(t time.Time) bool {
	if !s.minute[t.Minute()] {
		return false
	}
	if !s.hour[t.Hour()] {
		return false
	}
	if !s.dom[t.Day()] {
		return false
	}
	if !s.month[int(t.Month())] {
		return false
	}
	wd := int(t.Weekday()) // 0=Sunday
	if !s.dow[wd] {
		return false
	}
	return true
}

func (s *fieldSchedule) Next(from time.Time) time.Time {
	// Advance to next minute boundary.
	t := from.Truncate(time.Minute).Add(time.Minute)
	// Search up to 4 years to avoid infinite loops on impossible expressions.
	limit := t.Add(4 * 365 * 24 * time.Hour)
	for t.Before(limit) {
		if s.Matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// ─── @every interval schedule ────────────────────────────────────────────────

type intervalSchedule struct {
	interval time.Duration
	origin   time.Time // zero = daemon start epoch
}

func (s *intervalSchedule) Matches(t time.Time) bool {
	origin := s.origin
	if origin.IsZero() {
		// Align to Unix epoch minutes.
		origin = time.Unix(0, 0).UTC()
	}
	elapsed := t.Truncate(time.Minute).Sub(origin.Truncate(time.Minute))
	if elapsed < 0 {
		elapsed = -elapsed
	}
	intervalMinutes := int64(s.interval / time.Minute)
	if intervalMinutes <= 0 {
		intervalMinutes = 1
	}
	elapsedMinutes := int64(elapsed / time.Minute)
	return elapsedMinutes%intervalMinutes == 0
}

func (s *intervalSchedule) Next(from time.Time) time.Time {
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(4 * 365 * 24 * time.Hour)
	for t.Before(limit) {
		if s.Matches(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}
