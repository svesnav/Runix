// Package cron parses standard 5-field cron expressions and computes the
// next matching time. A small self-contained implementation avoids pulling a
// dependency for what is a well-defined, testable calculation.
//
// Format: minute hour day-of-month month day-of-week
//
//   - every value
//     5        an exact value
//     1-5      a range
//     */15     a step over the whole range
//     1-30/5   a step over a range
//     1,15,30  a list (each item may itself be a range or step)
//
// Day-of-week accepts 0-7 (both 0 and 7 mean Sunday). When both
// day-of-month and day-of-week are restricted, a time matches if *either*
// matches, which is the behaviour of Vixie cron.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	expr    string
	minutes uint64 // bit per minute 0-59
	hours   uint64 // bit per hour 0-23
	dom     uint64 // bit per day 1-31
	months  uint64 // bit per month 1-12
	dow     uint64 // bit per weekday 0-6
	domAny  bool
	dowAny  bool
}

func (s *Schedule) String() string { return s.expr }

// Common shorthands, accepted in place of a 5-field expression.
var shorthands = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 0 * * 0",
	"@monthly":  "0 0 1 * *",
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
}

func Parse(expr string) (*Schedule, error) {
	original := strings.TrimSpace(expr)
	normalized := original
	if expanded, ok := shorthands[strings.ToLower(original)]; ok {
		normalized = expanded
	}

	fields := strings.Fields(normalized)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields (minute hour day month weekday), got %d", len(fields))
	}

	s := &Schedule{expr: original}
	var err error
	if s.minutes, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("cron: minute: %w", err)
	}
	if s.hours, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("cron: hour: %w", err)
	}
	if s.dom, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("cron: day of month: %w", err)
	}
	if s.months, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("cron: month: %w", err)
	}
	if s.dow, err = parseWeekdays(fields[4]); err != nil {
		return nil, fmt.Errorf("cron: day of week: %w", err)
	}
	s.domAny = fields[2] == "*"
	s.dowAny = fields[4] == "*"
	return s, nil
}

func parseField(field string, minV, maxV int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(field, ",") {
		partBits, err := parsePart(part, minV, maxV)
		if err != nil {
			return 0, err
		}
		bits |= partBits
	}
	if bits == 0 {
		return 0, fmt.Errorf("%q matches nothing", field)
	}
	return bits, nil
}

func parsePart(part string, minV, maxV int) (uint64, error) {
	step := 1
	if base, stepStr, ok := strings.Cut(part, "/"); ok {
		n, err := strconv.Atoi(stepStr)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid step in %q", part)
		}
		step = n
		part = base
	}

	from, to := minV, maxV
	switch {
	case part == "*":
		// full range
	case strings.Contains(part, "-"):
		lo, hi, _ := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("invalid range %q", part)
		}
		from, to = a, b
	default:
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return 0, fmt.Errorf("invalid value %q", part)
		}
		from, to = n, n
	}

	if from < minV || to > maxV || from > to {
		return 0, fmt.Errorf("%q is out of range %d-%d", part, minV, maxV)
	}
	var bits uint64
	for v := from; v <= to; v += step {
		bits |= 1 << uint(v)
	}
	return bits, nil
}

func parseWeekdays(field string) (uint64, error) {
	bits, err := parseField(field, 0, 7)
	if err != nil {
		return 0, err
	}
	// 7 is an alias for Sunday.
	if bits&(1<<7) != 0 {
		bits |= 1 << 0
		bits &^= 1 << 7
	}
	return bits, nil
}

// Next returns the first matching time strictly after t, in t's location.
// It returns the zero time if no match exists within five years (which only
// happens for impossible dates such as 30 February).
func (s *Schedule) Next(t time.Time) time.Time {
	// Start from the next whole minute.
	next := t.Truncate(time.Minute).Add(time.Minute)
	limit := next.AddDate(5, 0, 0)

	for next.Before(limit) {
		if s.months&(1<<uint(next.Month())) == 0 {
			// Jump to the first day of the next month.
			next = time.Date(next.Year(), next.Month(), 1, 0, 0, 0, 0, next.Location()).
				AddDate(0, 1, 0)
			continue
		}
		if !s.matchesDay(next) {
			next = time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, next.Location()).
				AddDate(0, 0, 1)
			continue
		}
		if s.hours&(1<<uint(next.Hour())) == 0 {
			next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), 0, 0, 0, next.Location()).
				Add(time.Hour)
			continue
		}
		if s.minutes&(1<<uint(next.Minute())) == 0 {
			next = next.Add(time.Minute)
			continue
		}
		return next
	}
	return time.Time{}
}

// matchesDay implements the Vixie rule: when both day fields are
// restricted the match is a union, otherwise it is the restricted one.
func (s *Schedule) matchesDay(t time.Time) bool {
	domMatch := s.dom&(1<<uint(t.Day())) != 0
	dowMatch := s.dow&(1<<uint(int(t.Weekday()))) != 0
	switch {
	case s.domAny && s.dowAny:
		return true
	case s.domAny:
		return dowMatch
	case s.dowAny:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}
