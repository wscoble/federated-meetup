// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"strings"
	"testing"
	"time"
)

func TestParseRRULE_Basic(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		freq    string
		count   int
	}{
		{"FREQ=WEEKLY;COUNT=10", false, "WEEKLY", 10},
		{"FREQ=DAILY;INTERVAL=3", false, "DAILY", 0},
		{"FREQ=MONTHLY;BYDAY=MO", false, "MONTHLY", 0},
		{"FREQ=WEEKLY;BYDAY=MO,WE,FR;COUNT=5", false, "WEEKLY", 5},
		{"", true, "", 0},
		{"FREQ=HOURLY", true, "", 0}, // unsupported freq
		{"COUNT=10", true, "", 0},   // missing FREQ
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			p, err := ParseRRULE(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Freq != tt.freq {
				t.Errorf("freq: got %q, want %q", p.Freq, tt.freq)
			}
			if p.Count != tt.count {
				t.Errorf("count: got %d, want %d", p.Count, tt.count)
			}
		})
	}
}

func TestParseRRULE_Interval(t *testing.T) {
	p, err := ParseRRULE("FREQ=DAILY;INTERVAL=3")
	if err != nil {
		t.Fatal(err)
	}
	if p.Interval != 3 {
		t.Fatalf("interval: got %d, want 3", p.Interval)
	}
}

func TestParseRRULE_DefaultInterval(t *testing.T) {
	p, err := ParseRRULE("FREQ=WEEKLY")
	if err != nil {
		t.Fatal(err)
	}
	if p.Interval != 1 {
		t.Fatalf("default interval: got %d, want 1", p.Interval)
	}
}

func TestParseRRULE_Until(t *testing.T) {
	p, err := ParseRRULE("FREQ=DAILY;UNTIL=20260715")
	if err != nil {
		t.Fatal(err)
	}
	if p.Until == nil {
		t.Fatal("until should not be nil")
	}
	expected := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	if !p.Until.Equal(expected) {
		t.Fatalf("until: got %v, want %v", p.Until, expected)
	}
}

func TestParseRRULE_ByDay(t *testing.T) {
	p, err := ParseRRULE("FREQ=WEEKLY;BYDAY=MO,WE,FR")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ByDay) != 3 {
		t.Fatalf("byday: got %d items, want 3", len(p.ByDay))
	}
	if p.ByDay[0] != "MO" || p.ByDay[1] != "WE" || p.ByDay[2] != "FR" {
		t.Fatalf("byday: got %v", p.ByDay)
	}
}

func TestExpandInstances_Daily(t *testing.T) {
	p, _ := ParseRRULE("FREQ=DAILY;COUNT=5")
	start := time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 30)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 5 {
		t.Fatalf("got %d instances, want 5", len(instances))
	}
	for i, inst := range instances {
		expected := start.AddDate(0, 0, i)
		if !inst.Equal(expected) {
			t.Errorf("instance %d: got %v, want %v", i, inst, expected)
		}
	}
}

func TestExpandInstances_DailyInterval(t *testing.T) {
	p, _ := ParseRRULE("FREQ=DAILY;INTERVAL=3;COUNT=4")
	start := time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 30)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 4 {
		t.Fatalf("got %d instances, want 4", len(instances))
	}
	// Jul 1, Jul 4, Jul 7, Jul 10
	expected := []time.Time{
		time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 4, 18, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 7, 18, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 10, 18, 0, 0, 0, time.UTC),
	}
	for i, inst := range instances {
		if !inst.Equal(expected[i]) {
			t.Errorf("instance %d: got %v, want %v", i, inst, expected[i])
		}
	}
}

func TestExpandInstances_WeeklyByDay(t *testing.T) {
	p, _ := ParseRRULE("FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4")
	// Start on a Wednesday (July 1, 2026 is a Wednesday)
	start := time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 30)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 4 {
		t.Fatalf("got %d instances, want 4", len(instances))
	}
	// Jul 1 (Wed), Jul 6 (Mon), Jul 8 (Wed), Jul 13 (Mon)
	if !instances[0].Equal(start) {
		t.Errorf("instance 0: got %v, want %v", instances[0], start)
	}
	// All should be Monday or Wednesday
	for _, inst := range instances {
		wd := inst.Weekday()
		if wd != time.Monday && wd != time.Wednesday {
			t.Errorf("instance on %v should be Mon or Wed, got %v", inst, wd)
		}
	}
}

func TestExpandInstances_WeeklyNoByDay(t *testing.T) {
	p, _ := ParseRRULE("FREQ=WEEKLY;COUNT=3")
	start := time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 30)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 3 {
		t.Fatalf("got %d instances, want 3", len(instances))
	}
	// Each instance should be 7 days apart
	for i := 1; i < len(instances); i++ {
		diff := instances[i].Sub(instances[i-1])
		if diff != 7*24*time.Hour {
			t.Errorf("interval %d: got %v, want 7 days", i, diff)
		}
	}
}

func TestExpandInstances_Monthly(t *testing.T) {
	p, _ := ParseRRULE("FREQ=MONTHLY;COUNT=3")
	start := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	until := start.AddDate(1, 0, 0)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 3 {
		t.Fatalf("got %d instances, want 3", len(instances))
	}
	expected := []time.Time{
		time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 15, 18, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 15, 18, 0, 0, 0, time.UTC),
	}
	for i, inst := range instances {
		if !inst.Equal(expected[i]) {
			t.Errorf("instance %d: got %v, want %v", i, inst, expected[i])
		}
	}
}

func TestExpandInstances_Until(t *testing.T) {
	p, _ := ParseRRULE("FREQ=DAILY;UNTIL=20260705")
	start := time.Date(2026, 7, 1, 18, 0, 0, 0, time.UTC)
	until := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)

	instances := ExpandInstances(p, start, until)
	// UNTIL=20260705 means midnight July 5. Start is at 18:00.
	// So Jul 1 18:00, Jul 2 18:00, Jul 3 18:00, Jul 4 18:00 are <= UNTIL.
	// Jul 5 18:00 > Jul 5 00:00, so it's excluded.
	if len(instances) != 4 {
		t.Fatalf("got %d instances, want 4", len(instances))
	}
}

func TestExpandInstances_EmptyByDayFallsBackToWeekly(t *testing.T) {
	p, _ := ParseRRULE("FREQ=WEEKLY;COUNT=2")
	start := time.Date(2026, 7, 3, 18, 0, 0, 0, time.UTC) // Friday
	until := start.AddDate(0, 0, 30)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 2 {
		t.Fatalf("got %d instances, want 2", len(instances))
	}
	// Both should be Friday
	for _, inst := range instances {
		if inst.Weekday() != time.Friday {
			t.Errorf("expected Friday, got %v", inst.Weekday())
		}
	}
}

func TestExpandInstances_MonthlyClampsDay(t *testing.T) {
	// Jan 31 → Feb should clamp to Feb 28
	p, _ := ParseRRULE("FREQ=MONTHLY;COUNT=3")
	start := time.Date(2026, 1, 31, 18, 0, 0, 0, time.UTC)
	until := start.AddDate(1, 0, 0)

	instances := ExpandInstances(p, start, until)
	if len(instances) != 3 {
		t.Fatalf("got %d instances, want 3", len(instances))
	}
	// Jan 31, Feb 28, Mar 31
	if instances[0].Day() != 31 {
		t.Errorf("instance 0 day: got %d, want 31", instances[0].Day())
	}
	if instances[1].Day() != 28 {
		t.Errorf("instance 1 day: got %d, want 28 (Feb clamped)", instances[1].Day())
	}
	if instances[2].Day() != 31 {
		t.Errorf("instance 2 day: got %d, want 31", instances[2].Day())
	}
}

func TestExpandInstances_SafetyCap(t *testing.T) {
	p, _ := ParseRRULE("FREQ=DAILY") // no COUNT, no UNTIL
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC) // 10 years

	instances := ExpandInstances(p, start, until)
	if len(instances) > 1000 {
		t.Fatalf("safety cap failed: got %d instances (max 1000)", len(instances))
	}
}

func TestExpandInstances_TimePreserved(t *testing.T) {
	p, _ := ParseRRULE("FREQ=DAILY;COUNT=3")
	start := time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC)
	until := start.AddDate(0, 0, 30)

	instances := ExpandInstances(p, start, until)
	for i, inst := range instances {
		if inst.Hour() != 9 || inst.Minute() != 30 {
			t.Errorf("instance %d: time should be 9:30, got %v:%v", i, inst.Hour(), inst.Minute())
		}
	}
}

func TestParseRRULE_CaseInsensitive(t *testing.T) {
	p, err := ParseRRULE("freq=weekly;count=5")
	if err != nil {
		t.Fatal(err)
	}
	if p.Freq != "WEEKLY" {
		t.Fatalf("freq: got %q, want WEEKLY", p.Freq)
	}
	if p.Count != 5 {
		t.Fatalf("count: got %d, want 5", p.Count)
	}
}

func TestParseRRULE_InvalidCount(t *testing.T) {
	_, err := ParseRRULE("FREQ=DAILY;COUNT=0")
	if err == nil {
		t.Fatal("expected error for COUNT=0")
	}
	if !strings.Contains(err.Error(), "COUNT") {
		t.Fatalf("error should mention COUNT: %v", err)
	}
}