package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

// Date is a calendar date without time of day or time zone. It is held as midnight
// UTC so that day arithmetic counts calendar days and is unaffected by DST.
type Date struct {
	t time.Time
}

func NewDate(year int, month time.Month, day int) Date {
	return Date{t: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// ParseDate parses YYYY-MM-DD and rejects impossible dates such as 2026-02-30.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date %q: want YYYY-MM-DD", s)
	}
	return Date{t: t}, nil
}

// BusinessDate is the calendar date at instant now in the dealership's time zone.
func BusinessDate(now time.Time, loc *time.Location) Date {
	y, m, d := now.In(loc).Date()
	return NewDate(y, m, d)
}

func (d Date) IsZero() bool       { return d.t.IsZero() }
func (d Date) Year() int          { return d.t.Year() }
func (d Date) Before(o Date) bool { return d.t.Before(o.t) }
func (d Date) After(o Date) bool  { return d.t.After(o.t) }
func (d Date) AddDays(n int) Date { return Date{t: d.t.AddDate(0, 0, n)} }
func (d Date) String() string     { return d.t.Format(dateLayout) }

// DaysSince returns the number of calendar days from earlier to d.
func (d Date) DaysSince(earlier Date) int {
	return int(d.t.Sub(earlier.t) / (24 * time.Hour))
}

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte(`""`), nil
	}
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*d = Date{}
		return nil
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
