package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr string
		from string
		want string
	}{
		// Every minute.
		{"* * * * *", "2026-03-10 09:15", "2026-03-10 09:16"},
		// Top of every hour.
		{"0 * * * *", "2026-03-10 09:15", "2026-03-10 10:00"},
		// Specific time of day, rolls to tomorrow.
		{"30 2 * * *", "2026-03-10 09:15", "2026-03-11 02:30"},
		{"30 2 * * *", "2026-03-10 01:15", "2026-03-10 02:30"},
		// Steps.
		{"*/15 * * * *", "2026-03-10 09:16", "2026-03-10 09:30"},
		{"*/15 * * * *", "2026-03-10 09:46", "2026-03-10 10:00"},
		// Lists and ranges.
		{"0 9,17 * * *", "2026-03-10 10:00", "2026-03-10 17:00"},
		{"0 9-11 * * *", "2026-03-10 09:30", "2026-03-10 10:00"},
		{"0 0 1 * *", "2026-03-10 09:15", "2026-04-01 00:00"},
		// Weekday: 2026-03-10 is a Tuesday, next Sunday is the 15th.
		{"0 3 * * 0", "2026-03-10 09:15", "2026-03-15 03:00"},
		// 7 is Sunday too.
		{"0 3 * * 7", "2026-03-10 09:15", "2026-03-15 03:00"},
		// Month restriction rolls across the year.
		{"0 0 1 1 *", "2026-03-10 09:15", "2027-01-01 00:00"},
		// Shorthands.
		{"@daily", "2026-03-10 09:15", "2026-03-11 00:00"},
		{"@hourly", "2026-03-10 09:15", "2026-03-10 10:00"},
		{"@monthly", "2026-03-10 09:15", "2026-04-01 00:00"},
	}
	for _, c := range cases {
		got := mustParse(t, c.expr).Next(at(c.from))
		if !got.Equal(at(c.want)) {
			t.Errorf("Parse(%q).Next(%s) = %s, want %s",
				c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

// When both day fields are restricted, cron matches either one.
func TestDayOfMonthOrWeekdayUnion(t *testing.T) {
	s := mustParse(t, "0 0 13 * 5") // the 13th, or any Friday
	// 2026-03-10 is Tuesday; Friday the 13th satisfies both.
	if got := s.Next(at("2026-03-10 09:00")); !got.Equal(at("2026-03-13 00:00")) {
		t.Errorf("next = %s, want 2026-03-13 00:00", got.Format("2006-01-02 15:04"))
	}
	// From the 14th (Saturday), the next match is the 20th (Friday).
	if got := s.Next(at("2026-03-14 00:00")); !got.Equal(at("2026-03-20 00:00")) {
		t.Errorf("next = %s, want 2026-03-20 00:00", got.Format("2006-01-02 15:04"))
	}
}

func TestNextIsStrictlyAfter(t *testing.T) {
	s := mustParse(t, "*/5 * * * *")
	from := at("2026-03-10 09:05")
	if got := s.Next(from); !got.After(from) {
		t.Errorf("Next(%s) = %s, must be strictly later", from, got)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		"", "* * * *", "* * * * * *",
		"60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8",
		"5-1 * * * *", "*/0 * * * *", "abc * * * *", "* * * * mon",
	}
	for _, expr := range bad {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) accepted an invalid expression", expr)
		}
	}
}

func TestImpossibleDateReturnsZero(t *testing.T) {
	// 30 February never occurs.
	s := mustParse(t, "0 0 30 2 *")
	if got := s.Next(at("2026-03-10 09:00")); !got.IsZero() {
		t.Errorf("expected zero time for an impossible date, got %s", got)
	}
}
