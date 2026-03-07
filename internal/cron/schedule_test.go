package cron

import (
	"testing"
	"time"
)

func ts(year, month, day, hour, min int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, time.UTC)
}

// ─── Parse valid expressions ─────────────────────────────────────────────────

func TestParse_everyMinute(t *testing.T) {
	s, err := Parse("* * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, min := range []int{0, 15, 30, 59} {
		if !s.Matches(ts(2026, 3, 7, 10, min)) {
			t.Errorf("expected match at minute %d", min)
		}
	}
}

func TestParse_specificMinuteAndHour(t *testing.T) {
	s, err := Parse("30 9 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Matches(ts(2026, 3, 7, 9, 30)) {
		t.Error("expected match at 09:30")
	}
	if s.Matches(ts(2026, 3, 7, 9, 31)) {
		t.Error("should not match at 09:31")
	}
	if s.Matches(ts(2026, 3, 7, 10, 30)) {
		t.Error("should not match at 10:30")
	}
}

func TestParse_commaList(t *testing.T) {
	s, err := Parse("0,30 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Matches(ts(2026, 3, 7, 10, 0)) {
		t.Error("expected match at :00")
	}
	if !s.Matches(ts(2026, 3, 7, 10, 30)) {
		t.Error("expected match at :30")
	}
	if s.Matches(ts(2026, 3, 7, 10, 15)) {
		t.Error("should not match at :15")
	}
}

func TestParse_range(t *testing.T) {
	s, err := Parse("0 9-17 * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Matches(ts(2026, 3, 7, 9, 0)) {
		t.Error("expected match at 09:00")
	}
	if !s.Matches(ts(2026, 3, 7, 17, 0)) {
		t.Error("expected match at 17:00")
	}
	if s.Matches(ts(2026, 3, 7, 8, 0)) {
		t.Error("should not match at 08:00")
	}
	if s.Matches(ts(2026, 3, 7, 18, 0)) {
		t.Error("should not match at 18:00")
	}
}

func TestParse_step(t *testing.T) {
	s, err := Parse("*/15 * * * *")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, min := range []int{0, 15, 30, 45} {
		if !s.Matches(ts(2026, 3, 7, 12, min)) {
			t.Errorf("expected match at minute %d", min)
		}
	}
	if s.Matches(ts(2026, 3, 7, 12, 1)) {
		t.Error("should not match at minute 1")
	}
}

func TestParse_dowWeekdays(t *testing.T) {
	s, err := Parse("0 9 * * 1-5") // weekdays only
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2026-03-09 is Monday
	if !s.Matches(ts(2026, 3, 9, 9, 0)) {
		t.Error("expected match on Monday")
	}
	// 2026-03-07 is Saturday
	if s.Matches(ts(2026, 3, 7, 9, 0)) {
		t.Error("should not match on Saturday")
	}
}

// ─── Shorthands ───────────────────────────────────────────────────────────────

func TestParse_hourlyShorthand(t *testing.T) {
	s, err := Parse("@hourly")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Matches(ts(2026, 3, 7, 14, 0)) {
		t.Error("expected match at top of hour")
	}
	if s.Matches(ts(2026, 3, 7, 14, 1)) {
		t.Error("should not match at :01")
	}
}

func TestParse_dailyShorthand(t *testing.T) {
	s, err := Parse("@daily")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !s.Matches(ts(2026, 3, 7, 0, 0)) {
		t.Error("expected match at midnight")
	}
	if s.Matches(ts(2026, 3, 7, 1, 0)) {
		t.Error("should not match at 01:00")
	}
}

func TestParse_everyDuration(t *testing.T) {
	s, err := Parse("@every 30m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unix epoch is at minute 0; 30-minute multiples match
	if !s.Matches(time.Unix(0, 0).UTC()) {
		t.Error("expected match at epoch (minute 0)")
	}
	if !s.Matches(time.Unix(30*60, 0).UTC()) {
		t.Error("expected match at minute 30")
	}
	if s.Matches(time.Unix(15*60, 0).UTC()) {
		t.Error("should not match at minute 15")
	}
}

// ─── Parse errors ─────────────────────────────────────────────────────────────

func TestParse_wrongFieldCount(t *testing.T) {
	_, err := Parse("* * * *") // 4 fields
	if err == nil {
		t.Error("expected error for 4-field expression")
	}
}

func TestParse_outOfRange(t *testing.T) {
	_, err := Parse("60 * * * *") // minute 60 is invalid
	if err == nil {
		t.Error("expected error for minute=60")
	}
}

func TestParse_invalidStep(t *testing.T) {
	_, err := Parse("*/0 * * * *") // step=0 invalid
	if err == nil {
		t.Error("expected error for step=0")
	}
}

func TestParse_everyNegativeDuration(t *testing.T) {
	_, err := Parse("@every -5m")
	if err == nil {
		t.Error("expected error for negative @every")
	}
}

func TestParse_everyInvalidDuration(t *testing.T) {
	_, err := Parse("@every banana")
	if err == nil {
		t.Error("expected error for invalid @every duration")
	}
}

// ─── Next ─────────────────────────────────────────────────────────────────────

func TestFieldSchedule_Next(t *testing.T) {
	s, err := Parse("0 9 * * *") // daily at 09:00
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	from := ts(2026, 3, 7, 8, 45)
	next := s.Next(from)
	want := ts(2026, 3, 7, 9, 0)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", from, next, want)
	}
}

func TestFieldSchedule_Next_advancesDay(t *testing.T) {
	s, err := Parse("0 9 * * *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	from := ts(2026, 3, 7, 9, 1) // just past 09:00
	next := s.Next(from)
	want := ts(2026, 3, 8, 9, 0) // next day
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", from, next, want)
	}
}
