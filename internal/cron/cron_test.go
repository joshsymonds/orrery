package cron_test

import (
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/cron"
)

// layout formats the fire times the tests compare, as wall-clock date, hour and minute.
const layout = "2006-01-02 15:04"

// zoneLayout is layout with the zone abbreviation, for times that must show their location.
const zoneLayout = "2006-01-02 15:04 MST"

// utc returns the UTC time that layout formats as s.
func utc(t *testing.T, s string) time.Time {
	t.Helper()
	got, err := time.ParseInLocation(layout, s, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// mustParse returns the schedule for expr, failing the test when Parse refuses it.
func mustParse(t *testing.T, expr string) cron.Schedule {
	t.Helper()
	s, err := cron.Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

// checkFires fails the test unless the next len(want) fires of expr after the given time are want, in order.
func checkFires(t *testing.T, expr string, after time.Time, want []string) {
	t.Helper()
	s := mustParse(t, expr)
	got := make([]string, 0, len(want))
	for range want {
		after = s.Next(after)
		got = append(got, after.Format(layout))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%q fires = %v, want %v", expr, got, want)
	}
}

func TestCronParseFields(t *testing.T) {
	t.Parallel()
	start := utc(t, "2026-01-01 00:00")
	tests := []struct {
		name string
		expr string
		want []string
	}{
		{"single value", "5 * * * *", []string{"2026-01-01 00:05", "2026-01-01 01:05", "2026-01-01 02:05"}},
		{"star", "* * * * *", []string{"2026-01-01 00:01", "2026-01-01 00:02", "2026-01-01 00:03"}},
		{"comma list", "1,5,7 * * * *", []string{
			"2026-01-01 00:01", "2026-01-01 00:05", "2026-01-01 00:07", "2026-01-01 01:01",
		}},
		{"range", "10-12 * * * *", []string{
			"2026-01-01 00:10", "2026-01-01 00:11", "2026-01-01 00:12", "2026-01-01 01:10",
		}},
		{"step on star", "*/15 * * * *", []string{
			"2026-01-01 00:15", "2026-01-01 00:30", "2026-01-01 00:45", "2026-01-01 01:00",
		}},
		{"step on range", "0-30/10 * * * *", []string{
			"2026-01-01 00:10", "2026-01-01 00:20", "2026-01-01 00:30", "2026-01-01 01:00",
		}},
		{"step larger than the field", "1-59/9223372036854775807 * * * *", []string{
			"2026-01-01 00:01", "2026-01-01 01:01", "2026-01-01 02:01",
		}},
		{"list of steps and ranges", "0 */6,12-13 * * *", []string{
			"2026-01-01 06:00", "2026-01-01 12:00", "2026-01-01 13:00", "2026-01-01 18:00",
		}},
		{"day of month list", "0 0 1,15 * *", []string{
			"2026-01-15 00:00", "2026-02-01 00:00", "2026-02-15 00:00",
		}},
		{"month range and list", "0 0 1 1-2,12 *", []string{
			"2026-02-01 00:00", "2026-12-01 00:00", "2027-01-01 00:00",
		}},
		{"day of week range and step", "0 0 * * 1-5/2", []string{
			"2026-01-02 00:00", "2026-01-05 00:00", "2026-01-07 00:00",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkFires(t, tc.expr, start, tc.want)
		})
	}
}

func TestCronParseRefusesInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"four fields", "* * * *",
			"cron needs 5 fields (minute, hour, day of month, month, day of week), got 4"},
		{"six fields", "0 * * * * *",
			"cron needs 5 fields (minute, hour, day of month, month, day of week), got 6"},
		{"minute out of range", "60 * * * *", "minute: 60 is outside 0-59"},
		{"hour out of range", "* 24 * * *", "hour: 24 is outside 0-23"},
		{"day of month out of range", "* * 0 * *", "day of month: 0 is outside 1-31"},
		{"month out of range", "* * * 13 *", "month: 13 is outside 1-12"},
		{"day of week out of range", "* * * * 9", "day of week: 9 is outside 0-7"},
		{"reversed range", "* * * * 5-1", "day of week: range 5-1 is reversed"},
		{"step zero", "*/0 * * * *", "minute: step must be at least 1, got 0"},
		{"step on bare value", "5/2 * * * *", `minute: step "5/2" needs * or a range`},
		{"empty list item", "0 0 * * 1,,2", "day of week: empty list item"},
		{"name", "0 0 * * MON", `day of week: "MON" is not a number`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cron.Parse(tc.expr)
			if err == nil {
				t.Fatalf("Parse(%q) accepted the expression, want error %q", tc.expr, tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("Parse(%q) error = %q, want %q", tc.expr, err, tc.want)
			}
		})
	}
}

func TestCronNextAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		expr  string
		after time.Time
		want  string
	}{
		{"strictly after a fire at the start time", "30 10 * * *",
			utc(t, "2026-01-01 10:30"), "2026-01-02 10:30"},
		{"seconds past a fire minute do not count", "30 10 * * *",
			utc(t, "2026-01-01 10:30").Add(45 * time.Second), "2026-01-02 10:30"},
		{"a minute before a fire", "30 10 * * *", utc(t, "2026-01-01 10:29"), "2026-01-01 10:30"},
		{"crosses an hour", "5 * * * *", utc(t, "2026-01-01 10:05"), "2026-01-01 11:05"},
		{"crosses a day", "0 0 * * *", utc(t, "2026-01-01 23:30"), "2026-01-02 00:00"},
		{"crosses a month", "0 0 1 * *", utc(t, "2026-01-15 12:00"), "2026-02-01 00:00"},
		{"crosses a year", "0 0 1 1 *", utc(t, "2026-06-01 00:00"), "2027-01-01 00:00"},
		{"crosses a leap day", "0 0 29 2 *", utc(t, "2026-03-01 00:00"), "2028-02-29 00:00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := mustParse(t, tc.expr)
			got := s.Next(tc.after).Format(layout)
			if got != tc.want {
				t.Fatalf("Next(%s) for %q = %s, want %s", tc.after, tc.expr, got, tc.want)
			}
		})
	}
}

func TestCronNextSundaySevenAndZero(t *testing.T) {
	t.Parallel()
	start := utc(t, "2026-01-01 00:00")
	want := []string{"2026-01-04 12:00", "2026-01-11 12:00"}
	for _, expr := range []string{"0 12 * * 0", "0 12 * * 7"} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			checkFires(t, expr, start, want)
		})
	}
}

func TestCronNextEitherDayField(t *testing.T) {
	t.Parallel()
	start := utc(t, "2026-01-01 00:00")
	tests := []struct {
		name string
		expr string
		want []string
	}{
		{"both restricted: either the 13th or a Friday", "0 0 13 * 5", []string{
			"2026-01-02 00:00", "2026-01-09 00:00", "2026-01-13 00:00", "2026-01-16 00:00",
		}},
		{"only day of month restricted: Fridays do not count", "0 0 13 * *", []string{
			"2026-01-13 00:00", "2026-02-13 00:00",
		}},
		{"only day of week restricted: the 13th does not count", "0 0 * * 5", []string{
			"2026-01-02 00:00", "2026-01-09 00:00", "2026-01-16 00:00",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkFires(t, tc.expr, start, tc.want)
		})
	}
}

func TestCronNextInLocation(t *testing.T) {
	t.Parallel()
	jst := time.FixedZone("JST", 9*60*60)
	s := mustParse(t, "30 9 * * *")
	got := s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, jst)).Format(zoneLayout)
	if want := "2026-01-01 09:30 JST"; got != want {
		t.Fatalf("Next in JST = %s, want %s", got, want)
	}
}

func TestCronNextNeverMatches(t *testing.T) {
	t.Parallel()
	s := mustParse(t, "0 0 30 2 *")
	if got := s.Next(utc(t, "2026-01-01 00:00")); !got.IsZero() {
		t.Fatalf("Next for February 30 = %s, want the zero time", got)
	}
}
