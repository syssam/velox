package engine

import (
	"errors"
	"reflect"
	"testing"
)

// TestDBFieldsClassified: every field of DB is a mode, which sameMode
// compares, or handle state, which a joined handle supplies. A new field
// fails this test until it is put in one list; a mode left out of sameMode
// would be lost when a DB joins a transaction (the misuse of a WithoutPolicy
// copy with no reason was, before sameMode compared it).
func TestDBFieldsClassified(t *testing.T) {
	modes := map[string]bool{"bypass": true, "misuse": true, "rec": true}
	state := map[string]bool{
		"D": true, "Ex": true, "Rt": true, "Cfg": true, "InTx": true,
		"txDeadline": true, "txCause": true, "tx": true, "txWrote": true,
		"txShard": true, "txRoot": true, "txSelf": true, "pool": true,
		"owned": true, "batch": true, "src": true,
	}
	for f := range reflect.TypeFor[DB]().Fields() {
		if !modes[f.Name] && !state[f.Name] {
			t.Errorf("DB.%s is neither a mode (compare it in sameMode) nor handle state", f.Name)
		}
	}
	base := DB{}
	for name, change := range map[string]func(*DB){
		"bypass": func(d *DB) { d.bypass = "x" },
		"misuse": func(d *DB) { d.misuse = errors.New("x") },
		"rec":    func(d *DB) { d.rec = &Recording{} },
	} {
		c := base
		change(&c)
		if sameMode(&base, &c) {
			t.Errorf("sameMode ignores %s", name)
		}
	}
}
