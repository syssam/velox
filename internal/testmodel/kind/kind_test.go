package kind

import (
	"testing"
	"time"
)

func TestDecValue(t *testing.T) {
	v, err := Dec("12.50").Value()
	if err != nil || v != "12.50" {
		t.Errorf("Value = %v, %v; want the text 12.50", v, err)
	}
}

func TestDecScan(t *testing.T) {
	for _, c := range []struct {
		name string
		src  any
		want Dec
	}{
		{"string", "3.14", "3.14"},
		{"bytes", []byte("2.50"), "2.50"},
		{"int64", int64(-7), "-7"},
		{"float64", 1.5, "1.5"},
		{"whole float64", float64(2), "2"},
	} {
		var d Dec
		if err := d.Scan(c.src); err != nil || d != c.want {
			t.Errorf("%s: Scan = %q, %v; want %q", c.name, d, err, c.want)
		}
	}
	d := Dec("keep")
	if err := d.Scan(true); err == nil || d != "keep" {
		t.Errorf("Scan(bool) = %q, %v; want an error and the value unchanged", d, err)
	}
	if err := d.Scan(nil); err == nil {
		t.Error("Scan(nil): want an error")
	}
}

func TestMoment(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	v, err := Moment(at).Value()
	if got, ok := v.(time.Time); err != nil || !ok || !got.Equal(at) {
		t.Errorf("Value = %v, %v; want %v", v, err, at)
	}
	var m Moment
	if err := m.Scan(at); err != nil || !time.Time(m).Equal(at) {
		t.Errorf("Scan(time) = %v, %v; want %v", time.Time(m), err, at)
	}
	if err := m.Scan("2026-01-02"); err == nil {
		t.Error("Scan(string): want an error")
	}
	if !time.Time(m).Equal(at) {
		t.Errorf("a failed Scan changed the value to %v", time.Time(m))
	}
}
