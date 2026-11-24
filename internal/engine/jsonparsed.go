package engine

import "strings"

// jsonNumberText is a regular expression that a text matches exactly when
// it is a number in JSON's grammar with an exponent of at most two digits:
// an optional "-", then "0" or digits with no leading zero, then an
// optional fraction and exponent. "10", "-2.5", "1e3" and "1E+05" match;
// "+1", "01", ".5", "1.", "1e100", " 1" and "0x10" do not. A JSON number as
// PostgreSQL's ->> writes it matches. With text of at most 200 characters,
// the value is within float64's range. It holds no "?", which MySQL's
// driver would count as a parameter, and no backslash, which MySQL's
// NO_BACKSLASH_ESCAPES reads differently.
const jsonNumberText = `^-{0,1}(0|[1-9][0-9]*)([.][0-9]+){0,1}([eE][-+]{0,1}[0-9]{1,2}){0,1}$`

// jsonNumberMax is the longest text that JSONKindTextNumber reads.
const jsonNumberMax = "200"

// jsonYear is a year from 1000 to 9999, MySQL's DATETIME's range.
const jsonYear = `[1-9][0-9]{3}`

// jsonTimeText is a regular expression that a text matches exactly when it
// is an RFC 3339 time with a real date of a year from 1000 to 9999:
// YYYY-MM-DDTHH:MM:SS, then a fraction of one to nine digits or none, then
// Z or an offset from -14:59 to +14:59, SQLite's limit. The T and the Z are
// upper case; a leap second (60) does not match, as Go's time.Parse refuses
// it. Its rules on "?" and backslashes are jsonNumberText's.
const jsonTimeText = `^(` +
	jsonYear + `-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|` +
	jsonYear + `-(0[469]|11)-(0[1-9]|[12][0-9]|30)|` +
	jsonYear + `-02-(0[1-9]|1[0-9]|2[0-8])|` +
	`([1-9][0-9](0[48]|[2468][048]|[13579][26])|([2468][048]|[13579][26])00)-02-29)` +
	`T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]([.][0-9]{1,9}){0,1}(Z|[-+](0[0-9]|1[0-4]):[0-5][0-9])$`

// jsonTimeLast is the one date jsonTimeText matches that JSONKindTextTime
// does not read: an offset west of UTC moves its instant past 9999, which
// MySQL's DATETIME and SQLite's date functions cannot hold.
const jsonTimeLast = "'9999-12-31'"

// jsonParsedSQL is, by dialect and kind, the template of the value a
// JSONKindTextNumber or JSONKindTextTime path reads. In it @ and a letter
// stand for the value at the path (jsonParsed). A guard keeps every text
// but the ones the kind reads from the cast, in a CASE, so no text fails
// the statement: PostgreSQL's cast would fail on a bad text, and MySQL's
// would warn, which a write's Where under strict mode makes an error.
//
// PostgreSQL (@E the jsonb at the path, @T its text) casts a number's text,
// or a matching string, to numeric, as JSONKindNumber does; a time to
// timestamptz, which reads the offset. MySQL (@X the JSON at the path, @U
// its unquoted text) casts a number to DOUBLE, as JSONKindNumber does; a
// time's local part to DATETIME(6) less its offset in minutes, a DATETIME
// of UTC as velox stores a time. CONVERT_TZ would leave a time outside
// TIMESTAMP's range unconverted. REGEXP_LIKE's 'c' keeps case whatever the
// collation, and the second test refuses a final line break, before which
// ICU's "$" can also match. SQLite (@Y json_type at the path, @S its value)
// has no regular expressions: json_valid, which reads RFC 8259's grammar,
// and two GLOBs make a number's test. A time's is GLOBs, a round trip
// through strftime, which refuses a day past the month's end, a test of the
// hour, which strftime keeps at 24, and julianday, which refuses anything
// but a fraction between the seconds and the zone. Its time is velox's text
// of a time in UTC, to the nanosecond, which compares as the bound time
// does and which velox's SQLite rows parse into a time.Time: strftime reads
// the offset of the time without its fraction, which it would round to the
// millisecond, and the fraction is copied as it is written.
// TestJSONParsedText checks the regular expressions against Go's parsers;
// the integration test TestJSONTextParsedParity checks every dialect's form.
var jsonParsedSQL = [SQLite + 1][2]string{
	Postgres: {
		`CASE jsonb_typeof(@E) WHEN 'number' THEN (@T)::numeric WHEN 'string' THEN ` +
			`CASE WHEN length(@T) <= ` + jsonNumberMax + ` AND @T COLLATE "C" ~ '` + jsonNumberText + `' THEN CAST(@T AS numeric) END END`,
		`CASE WHEN jsonb_typeof(@E) = 'string' AND @T COLLATE "C" ~ '` + jsonTimeText + `' AND left(@T, 10) <> ` + jsonTimeLast +
			` THEN CAST(@T AS timestamptz) END`,
	},
	MySQL: {
		`CASE WHEN JSON_TYPE(@X) IN ('INTEGER', 'UNSIGNED INTEGER', 'DOUBLE', 'DECIMAL') THEN CAST(@X AS DOUBLE) ` +
			`WHEN JSON_TYPE(@X) = 'STRING' AND CHAR_LENGTH(@U) <= ` + jsonNumberMax + ` AND REGEXP_LIKE(@U, '` + jsonNumberText + `', 'c') ` +
			`AND NOT REGEXP_LIKE(@U, '[^-+.0-9eE]', 'c') THEN CAST(@U AS DOUBLE) END`,
		`CASE WHEN JSON_TYPE(@X) = 'STRING' AND REGEXP_LIKE(@U, '` + jsonTimeText + `', 'c') ` +
			`AND NOT REGEXP_LIKE(@U, '[^-+.:0-9TZ]', 'c') AND LEFT(@U, 10) <> ` + jsonTimeLast + ` THEN ` +
			`CASE WHEN RIGHT(@U, 1) = 'Z' THEN CAST(LEFT(@U, CHAR_LENGTH(@U) - 1) AS DATETIME(6)) ` +
			`ELSE CAST(LEFT(@U, CHAR_LENGTH(@U) - 6) AS DATETIME(6)) - INTERVAL ((CAST(SUBSTRING(@U, -5, 2) AS SIGNED) * 60 + ` +
			`CAST(RIGHT(@U, 2) AS SIGNED)) * IF(SUBSTRING(@U, -6, 1) = '-', -1, 1)) MINUTE END END`,
	},
	SQLite: {
		`CASE WHEN @Y IN ('integer', 'real') THEN @S WHEN @Y = 'text' AND length(@S) <= ` + jsonNumberMax +
			` AND json_valid(@S) AND @S NOT GLOB '*[^-+.0-9eE]*' AND @S NOT GLOB '*[eE]*[0-9][0-9][0-9]*' THEN CAST(@S AS REAL) END`,
		`CASE WHEN @Y = 'text' AND @S GLOB '[1-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T[0-9][0-9]:[0-9][0-9]:[0-9][0-9]*' ` +
			`AND @S NOT GLOB '*[^-+.:0-9TZ]*' ` +
			`AND (@S GLOB '*Z' AND length(@S) <= 30 OR @S GLOB '*[-+][01][0-9]:[0-5][0-9]' AND substr(@S, -5, 2) <= '14' AND length(@S) <= 35) ` +
			`AND strftime('%Y-%m-%dT%H:%M:%S', substr(@S, 1, 19)) = substr(@S, 1, 19) AND substr(@S, 12, 2) <= '23' AND julianday(@S) IS NOT NULL ` +
			`AND substr(@S, 1, 10) <> ` + jsonTimeLast + ` THEN ` +
			`strftime('%Y-%m-%d %H:%M:%S', substr(@S, 1, 19) || ` + sqliteZone + `) || '.' || ` +
			`substr(substr(@S, 21, max(length(@S) - 20 - length(` + sqliteZone + `), 0)) || '000000000', 1, 9) || '+00:00' END`,
	},
}

// sqliteZone is a time's zone on SQLite: what follows its seconds and
// fraction, Z or an offset.
const sqliteZone = `ltrim(substr(@S, 20), '.0123456789')`

// jsonParsed writes the value at path p in column i, of kind
// JSONKindTextNumber or JSONKindTextTime, from jsonParsedSQL. Each value
// at the path binds its keys, or MySQL's and SQLite's whole path, again.
func (w *writer) jsonParsed(i int32, p JSONPath) {
	tmpl := jsonParsedSQL[w.d][bit(p.Kind == JSONKindTextTime)]
	call := func(fn string) {
		w.str(fn + "(")
		w.colRef(i)
		if w.d == Postgres {
			for _, k := range p.Keys {
				w.str(", ")
				w.argOf(i, k)
			}
		} else {
			w.str(", ")
			w.argOf(i, p.quotedPath())
		}
		w.str(")")
	}
	for {
		at := strings.IndexByte(tmpl, '@')
		if at < 0 {
			w.str(tmpl)
			return
		}
		w.str(tmpl[:at])
		switch tmpl[at+1] {
		case 'E':
			call("jsonb_extract_path")
		case 'T':
			call("jsonb_extract_path_text")
		case 'X':
			call("JSON_EXTRACT")
		case 'U':
			w.str("JSON_UNQUOTE(")
			call("JSON_EXTRACT")
			w.str(")")
		case 'Y':
			call("json_type")
		case 'S':
			call("json_extract")
		}
		tmpl = tmpl[at+2:]
	}
}
