package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBusinessDateUsesDealershipTimeZone(t *testing.T) {
	now := time.Date(2026, 10, 29, 14, 0, 0, 0, time.UTC)
	cases := map[string]Date{
		"Australia/Sydney":    NewDate(2026, 10, 30), // UTC+11
		"Europe/London":       NewDate(2026, 10, 29),
		"America/Los_Angeles": NewDate(2026, 10, 29), // UTC-7
	}
	for zone, want := range cases {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		if got := BusinessDate(now, loc); got != want {
			t.Errorf("%s: got %s, want %s", zone, got, want)
		}
	}
}

func TestDaysSinceCountsCalendarDaysAcrossDST(t *testing.T) {
	// Europe/London ends BST on 2026-10-25: that local day lasts 25 hours.
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	before := BusinessDate(time.Date(2026, 10, 24, 23, 30, 0, 0, time.UTC), london) // 00:30 BST on the 25th
	after := BusinessDate(time.Date(2026, 10, 26, 0, 30, 0, 0, time.UTC), london)   // 00:30 GMT on the 26th
	if before != NewDate(2026, 10, 25) || after != NewDate(2026, 10, 26) {
		t.Fatalf("got %s and %s", before, after)
	}
	if got := after.DaysSince(before); got != 1 {
		t.Fatalf("DaysSince = %d, want 1", got)
	}
}

func TestAgeInDays(t *testing.T) {
	cases := []struct {
		from, to Date
		want     int
	}{
		{NewDate(2026, 10, 29), NewDate(2026, 10, 29), 0},
		{NewDate(2026, 7, 30), NewDate(2026, 10, 28), 90},
		{NewDate(2026, 7, 30), NewDate(2026, 10, 29), 91},
		{NewDate(2028, 2, 28), NewDate(2028, 3, 1), 2}, // leap year
		{NewDate(2027, 2, 28), NewDate(2027, 3, 1), 1}, // common year
		{NewDate(2025, 12, 31), NewDate(2026, 1, 1), 1},
	}
	for _, tc := range cases {
		if got := AgeInDays(tc.from, tc.to); got != tc.want {
			t.Errorf("AgeInDays(%s, %s) = %d, want %d", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestParseDate(t *testing.T) {
	for _, bad := range []string{"2026-02-30", "2027-02-29", "29/10/2026", "2026-10-29T00:00:00Z", ""} {
		if _, err := ParseDate(bad); err == nil {
			t.Errorf("ParseDate(%q): want error", bad)
		}
	}
	got, err := ParseDate("2028-02-29")
	if err != nil || got != NewDate(2028, 2, 29) {
		t.Errorf("got %s, %v", got, err)
	}
}

func TestDateJSON(t *testing.T) {
	type wrapper struct {
		D Date `json:"d"`
	}
	for _, in := range []wrapper{{NewDate(2026, 7, 30)}, {}} {
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var out wrapper
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if out != in {
			t.Fatalf("round trip %s: got %v", raw, out)
		}
	}
	if raw, _ := json.Marshal(wrapper{NewDate(2026, 7, 30)}); string(raw) != `{"d":"2026-07-30"}` {
		t.Fatalf("encoding = %s", raw)
	}
	var w wrapper
	if err := json.Unmarshal([]byte(`{"d":"2026-13-01"}`), &w); err == nil {
		t.Fatal("invalid date accepted")
	}
}

func TestActionTypeValid(t *testing.T) {
	for _, v := range []ActionType{ActionPriceReduction, ActionPromote, ActionTransfer, ActionAuction, ActionWholesale, ActionHold, ActionOther} {
		if !v.Valid() {
			t.Errorf("%s should be valid", v)
		}
	}
	for _, v := range []ActionType{"", "price_reduction", "DISCOUNT"} {
		if v.Valid() {
			t.Errorf("%q should be invalid", v)
		}
	}
}
