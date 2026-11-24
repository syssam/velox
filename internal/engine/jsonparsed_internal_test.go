package engine

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// parsedTexts are texts near the forms jsonNumberText and jsonTimeText
// match: valid ones with each character replaced by a few others, cut short
// and lengthened, and the days around the ends of months and leap days of
// years that are and are not leap years.
func parsedTexts() []string {
	var out []string
	for _, b := range []string{
		"2026-03-01T10:00:00+08:00", "2024-02-29T23:59:59.123456789Z", "1999-12-31T00:00:00.5-14:59",
		"10", "-2.5", "1e3", "0.25E-07", "123456789012345678901234567890",
	} {
		out = append(out, b)
		for i := range len(b) {
			for _, c := range "0912345+-.:TZtze \n" {
				out = append(out, b[:i]+string(c)+b[i+1:])
			}
			out = append(out, b[:i], b[:i]+"0"+b[i:])
		}
	}
	for _, y := range []string{"0999", "1000", "1200", "1900", "2000", "2023", "2024", "2100", "2400", "9996", "9999"} {
		for m := range 14 {
			for _, d := range []string{"00", "01", "28", "29", "30", "31", "32"} {
				out = append(out, y+"-"+strconv.Itoa(100 + m)[1:]+"-"+d+"T12:00:00Z")
			}
		}
	}
	for _, h := range []string{"00", "09", "10", "14", "15", "19", "20", "23", "24"} {
		out = append(out, "2026-03-01T"+h+":59:59Z", "2026-03-01T12:00:00+"+h+":00", "2026-03-01T12:00:00-"+h+":59")
	}
	return append(out, "-0", "1e99", "1e100", "1e-99", "1E+05", strings.Repeat("9", 200), strings.Repeat("9", 201), "Infinity", "0x10", `"1"`)
}

// TestJSONParsedText: the guards of JSONKindTextNumber and JSONKindTextTime
// on PostgreSQL and MySQL, a regular expression and a length or the last
// date, match a text exactly when Go reads it: as JSON's number grammar,
// and as time.Parse reads RFC 3339, within the documented limits. The
// expressions use only what RE2, PostgreSQL's and ICU's (MySQL's) forms
// read alike, and no "?", backslash, quote or @ (jsonParsed's marker).
func TestJSONParsedText(t *testing.T) {
	num, tm := regexp.MustCompile(jsonNumberText), regexp.MustCompile(jsonTimeText)
	for _, re := range []string{jsonNumberText, jsonTimeText} {
		if strings.ContainsAny(re, `?\'@`) {
			t.Errorf("%s holds a ?, a backslash, a quote or an @", re)
		}
	}
	numberRef := func(s string) bool {
		if s == "" || len(s) > 200 || !json.Valid([]byte(s)) || strings.Trim(s, "-+.0123456789eE") != "" {
			return false
		}
		if i := strings.IndexAny(s, "eE"); i >= 0 && len(strings.TrimLeft(s[i+1:], "+-")) > 2 {
			return false
		}
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}
	timeRef := func(s string) bool {
		_, err := time.Parse(time.RFC3339Nano, s)
		if err != nil || len(s) < 20 || s[10] != 'T' || strings.ContainsAny(s, "tz \n") || s[0] == '0' {
			return false
		}
		zone := "Z"
		if !strings.HasSuffix(s, "Z") {
			if zone = s[len(s)-6:]; zone[1:3] > "14" {
				return false
			}
		}
		frac := s[19 : len(s)-len(zone)]
		return frac == "" || len(frac) >= 2 && len(frac) <= 10
	}
	nums, times := 0, 0
	for _, s := range parsedTexts() {
		if got, want := num.MatchString(s) && len(s) <= 200, numberRef(s); got != want {
			t.Errorf("number %q: matched %v, want %v", s, got, want)
		} else if got {
			nums++
		}
		if got, want := tm.MatchString(s), timeRef(s); got != want {
			t.Errorf("time %q: matched %v, want %v", s, got, want)
		} else if got {
			times++
		}
	}
	if nums < 30 || times < 100 {
		t.Errorf("%d numbers and %d times matched: too few valid texts", nums, times)
	}
}
