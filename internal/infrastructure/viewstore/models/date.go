// Package models holds the PostgreSQL row types of the read_model schema: column
// mapping and SQL null handling only, no behaviour. repositories maps them to the
// application models.
package models

import (
	"database/sql/driver"
	"fmt"
	"time"
)

const dateLayout = "2006-01-02"

// Date is a DATE column as a calendar date (no time of day, no zone). It is bound as
// 'YYYY-MM-DD' text, so the stored value never depends on how a driver converts a
// time.Time.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func (d Date) String() string {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC).Format(dateLayout)
}

func (d Date) Value() (driver.Value, error) {
	return d.String(), nil
}

func (d *Date) Scan(src interface{}) error {
	switch v := src.(type) {
	case time.Time:
		*d = Date{
			Year:  v.Year(),
			Month: v.Month(),
			Day:   v.Day(),
		}
		return nil
	case string:
		return d.parse(v)
	case []byte:
		return d.parse(string(v))
	}
	return fmt.Errorf("models.Date: cannot scan %T", src)
}

func (d *Date) parse(s string) error {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return fmt.Errorf("models.Date: %w", err)
	}
	*d = Date{
		Year:  t.Year(),
		Month: t.Month(),
		Day:   t.Day(),
	}
	return nil
}

// NullDate is a nullable DATE column.
type NullDate struct {
	Date  Date
	Valid bool
}

func (n NullDate) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Date.Value()
}

func (n *NullDate) Scan(src interface{}) error {
	if src == nil {
		*n = NullDate{}
		return nil
	}
	n.Valid = true
	return n.Date.Scan(src)
}
