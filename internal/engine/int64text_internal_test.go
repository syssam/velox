package engine

import (
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestInt64Text: the guard of a text column against an integer key (ADR
// 0161) matches a text exactly when it is an int64 as strconv.FormatInt
// writes it. The texts are the boundaries, every digit at every place of
// int64's extremes with the rest all 0s or all 9s, and forms that text
// equality with a key never matched: "07", " 7", "+7", "7.0", "-0", "".
func TestInt64Text(t *testing.T) {
	re := regexp.MustCompile(int64Text)
	canonical := func(s string) bool {
		v, err := strconv.ParseInt(s, 10, 64)
		return err == nil && strconv.FormatInt(v, 10) == s
	}
	texts := []string{
		"", "0", "-0", "00", "07", " 7", "7 ", "+7", "7.0", "1e3", "7\n", "-", "--7", "abc", "0x1F", "٧",
		"9223372036854775807", "9223372036854775808", "9223372036854775806", "9223372036854775810",
		"-9223372036854775808", "-9223372036854775809", "-9223372036854775807",
		"9999999999999999999", "10000000000000000000", "-10000000000000000000", "99999999999999999999",
		"1000000000000000000", "999999999999999999", "-999999999999999999",
	}
	for _, v := range []int64{math.MaxInt64, math.MinInt64, math.MaxInt64 - 1, math.MinInt64 + 1, 1, -1, 5, -5, math.MaxInt32, math.MaxInt32 + 1, math.MinInt32 - 1} {
		texts = append(texts, strconv.FormatInt(v, 10))
	}
	for k := 1; k <= 19; k++ {
		texts = append(texts, strings.Repeat("9", k), "1"+strings.Repeat("0", k-1))
	}
	for _, edge := range []string{"9223372036854775807", "9223372036854775808"} {
		for k := range len(edge) {
			for d := '0'; d <= '9'; d++ {
				for _, fill := range []string{"0", "9"} {
					s := edge[:k] + string(d) + strings.Repeat(fill, len(edge)-k-1)
					texts = append(texts, s, "-"+s)
				}
			}
		}
	}
	matched := 0
	for _, s := range texts {
		got, want := re.MatchString(s), canonical(s)
		if got != want {
			t.Errorf("%q: the guard says %v, strconv %v", s, got, want)
		}
		if got {
			matched++
		}
	}
	if matched < 100 || matched == len(texts) {
		t.Errorf("%d of %d texts matched: the cases do not reach both sides", matched, len(texts))
	}
}

// int64Key is true for a signed key and an unsigned one of 32 bits or
// fewer, whose values are all int64s; an unsigned 64-bit key keeps the text
// compare (ADR 0161).
func TestInt64Key(t *testing.T) {
	for _, c := range []struct {
		v    any
		want bool
	}{
		{int64(0), true}, {int32(0), true}, {int8(0), true}, {0, true}, {uint32(0), true}, {uint8(0), true},
		{new(int64), true}, {uint64(0), false}, {uint(0), false}, {new(uint64), false},
	} {
		if got := int64Key(ColumnInfo{Type: reflect.TypeOf(c.v)}); got != c.want {
			t.Errorf("%T: %v, want %v", c.v, got, c.want)
		}
	}
}
