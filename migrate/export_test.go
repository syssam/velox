package migrate

import (
	"strings"

	"github.com/syssam/velox2"
)

// For the tests of package migrate_test.
var (
	ApplyChanges = applyChanges
	Clone        = (*Schema).clone
)

// WriteScript is writeScript, for the goldens.
func WriteScript(s Script) string {
	var b strings.Builder
	writeScript(&b, s)
	return b.String()
}

// A Piece is a piece of split.
type Piece struct {
	SQL, Directive string
	Line           int
}

// Split is split.
func Split(d velox.Dialect, src string) ([]Piece, error) {
	ps, err := split(d, src)
	var out []Piece
	for _, p := range ps {
		out = append(out, Piece(p))
	}
	return out, err
}

// FileBackslash is f.backslash.
func FileBackslash(f File) bool { return f.backslash }

// FileGenerated is f.generated.
func FileGenerated(f File) bool { return f.generated }

// FileLines is f.lines.
func FileLines(f File) []int { return f.lines }

// FileDquote is f.dquote.
func FileDquote(f File) bool { return f.dquote }

// DefaultName is defaultName.
var DefaultName = defaultName

// Generator is generator.
var Generator = generator

// ConfigJSON is the velox.json Add writes for c.
func ConfigJSON(c Config) []byte { return c.marshal() }
