// SPDX-License-Identifier: AGPL-3.0

package product

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// RecurrencePattern represents a parsed RFC 5545 RRULE.
// Supports: FREQ, COUNT, UNTIL, BYDAY, INTERVAL.
type RecurrencePattern struct {
	Freq     string // DAILY, WEEKLY, MONTHLY
	Count    int    // 0 = unlimited (UNTIL or no limit)
	Until    *time.Time
	ByDay    []string // MO, TU, WE, TH, FR, SA, SU
	Interval int      // default 1
}

// ParseRRULE parses an RFC 5545 RRULE string into a RecurrencePattern.
// Supports: FREQ=WEEKLY, FREQ=MONTHLY, FREQ=DAILY, COUNT, UNTIL, BYDAY, INTERVAL.
// Example: "FREQ=WEEKLY;BYDAY=MO;COUNT=10"
func ParseRRULE(rule string) (RecurrencePattern, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return RecurrencePattern{}, fmt.Errorf("recurrence: empty RRULE")
	}

	pattern := RecurrencePattern{
		Interval: 1,
	}

	parts := strings.Split(rule, ";")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(kv[0]))
		val := strings.TrimSpace(kv[1])

		switch key {
		case "FREQ":
			freq := strings.ToUpper(val)
			switch freq {
			case "DAILY", "WEEKLY", "MONTHLY":
				pattern.Freq = freq
			default:
				return RecurrencePattern{}, fmt.Errorf("recurrence: unsupported FREQ %q", val)
			}
		case "COUNT":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return RecurrencePattern{}, fmt.Errorf("recurrence: invalid COUNT %q", val)
			}
			pattern.Count = n
		case "UNTIL":
			t, err := parseICSDate(val)
			if err != nil {
				return RecurrencePattern{}, fmt.Errorf("recurrence: invalid UNTIL %q: %w", val, err)
			}
			pattern.Until = &t
		case "BYDAY":
			for _, d := range strings.Split(val, ",") {
				d = strings.ToUpper(strings.TrimSpace(d))
				if isValidDayCode(d) {
					pattern.ByDay = append(pattern.ByDay, d)
				}
			}
		case "INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return RecurrencePattern{}, fmt.Errorf("recurrence: invalid INTERVAL %q", val)
			}
			pattern.Interval = n
		}
	}

	if pattern.Freq == "" {
		return RecurrencePattern{}, fmt.Errorf("recurrence: FREQ is required")
	}

	return pattern, nil
}

// parseICSDate parses an iCalendar date: YYYYMMDD or YYYYMMDDTHHMMSSZ.
// Date-only values (YYYYMMDD) are parsed as midnight UTC, not local time.
func parseICSDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	// Date-only: parse as UTC midnight (not local).
	if len(s) == 8 {
		if t, err := time.ParseInLocation("20060102", s, time.UTC); err == nil {
			return t.UTC(), nil
		}
	}
	layouts := []string{
		"20060102T150405Z",
		"20060102T150405",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date format")
}

// dayToWeekday maps day codes to time.Weekday values.
var dayToWeekday = map[string]time.Weekday{
	"SU": time.Sunday,
	"MO": time.Monday,
	"TU": time.Tuesday,
	"WE": time.Wednesday,
	"TH": time.Thursday,
	"FR": time.Friday,
	"SA": time.Saturday,
}

// weekdayToDay maps time.Weekday to day codes.
var weekdayToDay = map[time.Weekday]string{
	time.Sunday:    "SU",
	time.Monday:    "MO",
	time.Tuesday:   "TU",
	time.Wednesday: "WE",
	time.Thursday:  "TH",
	time.Friday:    "FR",
	time.Saturday:  "SA",
}

func isValidDayCode(d string) bool {
	_, ok := dayToWeekday[d]
	return ok
}

// ExpandInstances generates individual occurrence times from a RecurrencePattern,
// starting at `start` up to (but not after) `until` or the pattern's own UNTIL/COUNT limit.
// The `start` time itself is always the first instance.
//
// A safety cap of 1000 instances prevents runaway expansion.
func ExpandInstances(pattern RecurrencePattern, start time.Time, until time.Time) []time.Time {
	var instances []time.Time

	// Determine the effective end: the earlier of `until` and pattern.Until.
	effectiveEnd := until.UTC()
	if pattern.Until != nil && pattern.Until.Before(effectiveEnd) {
		effectiveEnd = pattern.Until.UTC()
	}

	// Safety cap to prevent runaway expansion.
	const maxInstances = 1000

	addInstance := func(t time.Time) bool {
		if len(instances) >= maxInstances {
			return false
		}
		if t.After(effectiveEnd) {
			return false
		}
		instances = append(instances, t.UTC())
		return true
	}

	switch pattern.Freq {
	case "DAILY":
		current := start.UTC()
		for i := 0; ; i++ {
			if pattern.Count > 0 && i >= pattern.Count {
				break
			}
			if current.After(effectiveEnd) {
				break
			}
			if !addInstance(current) {
				break
			}
			current = current.AddDate(0, 0, pattern.Interval)
		}

	case "WEEKLY":
		if len(pattern.ByDay) > 0 {
			// For BYDAY, we iterate week-by-week and include matching days.
			// Start from the beginning of the week containing `start`.
			weekStart := start.UTC()
			// Move to the Sunday of the starting week.
			weekStart = weekStart.AddDate(0, 0, -int(weekStart.Weekday()))
			// Restore the time-of-day from `start`.
			weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(),
				start.UTC().Hour(), start.UTC().Minute(), start.UTC().Second(), 0, time.UTC)

			count := 0
			weekOffset := 0
			for {
				anyAdded := false
				for _, dayCode := range pattern.ByDay {
					wd := dayToWeekday[dayCode]
					// Calculate the date for this day in the current week.
					dayDate := weekStart.AddDate(0, 0, int(wd))
					if dayDate.Before(start.UTC()) {
						continue // skip dates before the start
					}
					if dayDate.After(effectiveEnd) {
						continue
					}
					if pattern.Count > 0 && count >= pattern.Count {
						return instances
					}
					instances = append(instances, dayDate)
					count++
					anyAdded = true
					if len(instances) >= maxInstances {
						return instances
					}
				}
				if !anyAdded && weekOffset > 0 {
					// Check if we're past the end entirely.
					weekStart = weekStart.AddDate(0, 0, 7*pattern.Interval)
					if weekStart.After(effectiveEnd) {
						break
					}
				} else {
					weekStart = weekStart.AddDate(0, 0, 7*pattern.Interval)
				}
				weekOffset++
				if weekStart.After(effectiveEnd) && (pattern.Count == 0 || count >= pattern.Count) {
					break
				}
				// Safety: if we've gone way too many weeks without anything.
				if weekOffset > 500 {
					break
				}
			}
		} else {
			// No BYDAY — repeat every N weeks from start.
			current := start.UTC()
			for i := 0; ; i++ {
				if pattern.Count > 0 && i >= pattern.Count {
					break
				}
				if current.After(effectiveEnd) {
					break
				}
				if !addInstance(current) {
					break
				}
				current = current.AddDate(0, 0, 7*pattern.Interval)
			}
		}

	case "MONTHLY":
		// For MONTHLY, we iterate by adding N months to the start date.
		// We keep the same day-of-month as the start, clamping to the
		// last valid day of the target month.
		targetDay := start.UTC().Day()
		current := start.UTC()
		for i := 0; ; i++ {
			if pattern.Count > 0 && i >= pattern.Count {
				break
			}
			if current.After(effectiveEnd) {
				break
			}
			if !addInstance(current) {
				break
			}
			// Add N months, keeping the original target day (clamped).
			year := current.Year()
			month := int(current.Month()) + pattern.Interval
			for month > 12 {
				month -= 12
				year++
			}
			lastDay := lastDayOfMonth(year, time.Month(month))
			day := targetDay
			if day > lastDay {
				day = lastDay
			}
			current = time.Date(year, time.Month(month), day,
				current.Hour(), current.Minute(), current.Second(), 0, time.UTC)
		}
	}

	return instances
}

// lastDayOfMonth returns the last day of the given month/year.
func lastDayOfMonth(year int, month time.Month) int {
	// Day 0 of next month = last day of this month.
	t := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC)
	return t.Day()
}