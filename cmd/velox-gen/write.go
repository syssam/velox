package main

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"golang.org/x/tools/go/packages"
)

// result is what a run would leave on disk.
type result struct {
	write  map[string][]byte // path → the content the file must have
	remove []string          // files velox-gen wrote that no entity produces now
}

// plan works out every file of a run without touching the disk.
func plan(ps []*packages.Package, cfg config) (result, []problem) {
	res := result{write: map[string][]byte{}}
	var probs []problem
	for _, p := range ps {
		ents, pr := discover(p, cfg)
		probs = append(probs, pr...)
		probs = append(probs, name(p, ents)...)
		for _, e := range ents {
			if _, err := os.Stat(e.out); err == nil && !isGenerated(e.out) {
				probs = append(probs, problem{e.pos, fmt.Sprintf("%s: %s exists and velox-gen did not write it", e.name, e.out)})
				continue
			}
			src, err := render(e)
			if err != nil {
				probs = append(probs, problem{e.pos, fmt.Sprintf("%s: the generated file does not format: %v", e.name, err)})
				continue
			}
			res.write[e.out] = src
		}
		for _, f := range p.GoFiles {
			if _, ok := res.write[f]; !ok && isGenerated(f) {
				res.remove = append(res.remove, f)
			}
		}
	}
	sort.Strings(res.remove)
	return res, probs
}

// stale lists the files a run would change: written with other content, or
// removed.
func stale(res result) []string {
	var out []string
	for path, want := range res.write {
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
			out = append(out, path)
		}
	}
	out = append(out, res.remove...)
	sort.Strings(out)
	return out
}

// apply writes and removes the files of a run and returns the paths it
// changed. A file whose content is already right is left alone.
func apply(res result) ([]string, error) {
	changed := stale(res)
	for _, path := range changed {
		want, ok := res.write[path]
		if !ok {
			if err := os.Remove(path); err != nil {
				return nil, err
			}
			continue
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			return nil, err
		}
	}
	return changed, nil
}
