package main

import (
	"fmt"
	"strings"
)

// pluginSpec is one -plugin flag: name[:key=value[,key=value]...].
type pluginSpec struct {
	name    string            // the tool name
	options map[string]string // nil for none
	raw     string            // the flag as given, for the -check hint
}

// pluginFlag is the repeated -plugin flag, in command-line order.
type pluginFlag []pluginSpec

func (p *pluginFlag) String() string {
	raws := make([]string, len(*p))
	for i, s := range *p {
		raws[i] = s.raw
	}
	return strings.Join(raws, ",")
}

// Set parses name[:key=value[,key=value]...]. A key is not empty, has an =,
// and is given once; a value may be empty, or hold an =.
func (p *pluginFlag) Set(raw string) error {
	name, opts, hasOpts := strings.Cut(raw, ":")
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a plugin needs a tool name")
	}
	spec := pluginSpec{name: name, raw: raw}
	if hasOpts {
		spec.options = map[string]string{}
		for opt := range strings.SplitSeq(opts, ",") {
			k, v, ok := strings.Cut(opt, "=")
			switch {
			case !ok:
				return fmt.Errorf("plugin %s: option %q is not key=value", name, opt)
			case k == "":
				return fmt.Errorf("plugin %s: option %q has no key", name, opt)
			}
			if _, dup := spec.options[k]; dup {
				return fmt.Errorf("plugin %s: option %s is given twice", name, k)
			}
			spec.options[k] = v
		}
	}
	*p = append(*p, spec)
	return nil
}
