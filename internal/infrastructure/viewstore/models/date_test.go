package models

import (
	"testing"
	"time"
)

func TestDateIsBoundAsTextAndScansFromDriverValues(t *testing.T) {
	d := Date{
		Year:  2024,
		Month: time.February,
		Day:   29,
	}
	if v, err := d.Value(); err != nil || v != "2024-02-29" {
		t.Fatalf("value %v %v", v, err)
	}
	// The driver returns a DATE as midnight UTC; only its calendar fields are kept.
	for _, src := range []interface{}{
		time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC),
		"2024-02-29",
		[]byte("2024-02-29"),
	} {
		var got Date
		if err := got.Scan(src); err != nil || got != d {
			t.Fatalf("scan %T: %+v %v", src, got, err)
		}
	}
	var bad Date
	if err := bad.Scan(42); err == nil {
		t.Fatal("scanning an int must fail")
	}
}

func TestNullDate(t *testing.T) {
	var n NullDate
	if err := n.Scan(nil); err != nil || n.Valid {
		t.Fatalf("NULL: %+v %v", n, err)
	}
	if v, err := n.Value(); err != nil || v != nil {
		t.Fatalf("NULL value %v %v", v, err)
	}
	if err := n.Scan("2026-01-01"); err != nil || !n.Valid || n.Date.String() != "2026-01-01" {
		t.Fatalf("date: %+v %v", n, err)
	}
}
