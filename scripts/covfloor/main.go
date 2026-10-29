// Command covfloor checks per-package statement coverage against floors.
//
// It reads Go coverage profiles (go test -coverprofile), possibly from several
// modules, merges their blocks by file and position (a block counted in any
// profile is covered), and computes each package's coverage as covered
// statements over all statements. The floors file has one
// "<import path> <percent>" per line; '#' starts a comment.
//
//	go run ./scripts/covfloor -floors scripts/coverage-floors.txt cover-*.out
//
// It fails, naming every problem, when a package is under its floor, when a
// package in the profiles has no floor, and when a floor names a package no
// profile has.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

func main() {
	floors := flag.String("floors", "scripts/coverage-floors.txt", "the floors file")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: covfloor [-floors file] profile...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	problems, err := run(*floors, flag.Args(), os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covfloor:", err)
		os.Exit(2)
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		fmt.Fprintf(os.Stderr, "covfloor: %d problem(s); floors are in %s\n", len(problems), *floors)
		os.Exit(1)
	}
}

// run checks the profiles against the floors file and returns the problems.
func run(floorsFile string, profiles []string, out io.Writer) ([]string, error) {
	f, err := os.Open(floorsFile)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	floors, err := parseFloors(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", floorsFile, err)
	}
	var readers []io.Reader
	for _, p := range profiles {
		pf, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer func() { _ = pf.Close() }()
		readers = append(readers, pf)
	}
	cov, err := mergeProfiles(readers)
	if err != nil {
		return nil, err
	}
	problems := check(cov, floors, floorsFile)
	_, _ = fmt.Fprintf(out, "covfloor: %d packages checked against %s\n", len(cov), floorsFile)
	return problems, nil
}

// pkgCov is a package's statement counts.
type pkgCov struct{ covered, total int }

func (c pkgCov) percent() float64 {
	if c.total == 0 {
		return 0
	}
	return 100 * float64(c.covered) / float64(c.total)
}

// rounded is the percentage to one decimal, as the messages print it and as
// go test prints it, so that the comparison with a floor is the one the reader
// sees: no "50.0% < 50.0%".
func (c pkgCov) rounded() float64 {
	r, _ := strconv.ParseFloat(fmt.Sprintf("%.1f", c.percent()), 64)
	return r
}

// parseFloors reads "<import path> <percent>" lines.
func parseFloors(r io.Reader) (map[string]float64, error) {
	floors := map[string]float64{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: want \"<import path> <percent>\", got %q", n, strings.TrimSpace(line))
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || v < 0 || v > 100 {
			return nil, fmt.Errorf("line %d: %q is not a percentage between 0 and 100", n, fields[1])
		}
		if _, dup := floors[fields[0]]; dup {
			return nil, fmt.Errorf("line %d: %s has two floors", n, fields[0])
		}
		floors[fields[0]] = v
	}
	return floors, sc.Err()
}

// block is one profile line's identity and size.
type block struct {
	stmts   int
	covered bool
}

// mergeProfiles merges the profiles' blocks by "file:start,end" and sums each
// package's statements. The package of a file is its import path's directory.
func mergeProfiles(rs []io.Reader) (map[string]pkgCov, error) {
	blocks := map[string]*block{}
	for _, r := range rs {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		first := true
		for sc.Scan() {
			line := sc.Text()
			if first {
				first = false
				if strings.HasPrefix(line, "mode:") {
					continue
				}
			}
			if line == "" {
				continue
			}
			// file:startLine.startCol,endLine.endCol numStmt count
			sp := strings.Fields(line)
			if len(sp) != 3 {
				return nil, fmt.Errorf("malformed profile line %q", line)
			}
			stmts, err1 := strconv.Atoi(sp[1])
			count, err2 := strconv.Atoi(sp[2])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("malformed profile line %q", line)
			}
			b := blocks[sp[0]]
			if b == nil {
				b = &block{stmts: stmts}
				blocks[sp[0]] = b
			}
			if count > 0 {
				b.covered = true
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	cov := map[string]pkgCov{}
	for key, b := range blocks {
		i := strings.LastIndexByte(key, ':')
		if i < 0 {
			return nil, fmt.Errorf("malformed block %q", key)
		}
		pkg := path.Dir(key[:i])
		c := cov[pkg]
		c.total += b.stmts
		if b.covered {
			c.covered += b.stmts
		}
		cov[pkg] = c
	}
	return cov, nil
}

// check returns one message per problem, sorted by package.
func check(cov map[string]pkgCov, floors map[string]float64, floorsFile string) []string {
	var problems []string
	for _, pkg := range sortedKeys(cov) {
		got := cov[pkg].rounded()
		floor, ok := floors[pkg]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("add a floor for %s: measured %.1f%% (add the line \"%s %.1f\" to %s)", pkg, got, pkg, max(got-1, 0), floorsFile))
		case got < floor:
			problems = append(problems, fmt.Sprintf("%s fell below its floor: %.1f%% < %.1f%% (short by %.1f points); add tests, or if the drop is deliberate lower its line in %s to %.1f", pkg, got, floor, floor-got, floorsFile, max(got-1, 0)))
		}
	}
	for _, pkg := range sortedKeys(floors) {
		if _, ok := cov[pkg]; !ok {
			problems = append(problems, fmt.Sprintf("%s has a floor but no coverage: remove it from %s", pkg, floorsFile))
		}
	}
	return problems
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
