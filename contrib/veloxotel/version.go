package veloxotel

import (
	"runtime/debug"
	"sync"
)

// modulePath is this module's path: the instrumentation scope's name.
const modulePath = "github.com/syssam/velox/contrib/veloxotel"

// moduleVersion is the version of this module that bi records: the main
// module's when this is the main module, else the dependency's, or its
// replacement's when it is replaced. "(devel)" and a replacement by a
// directory, which has no version, give "".
func moduleVersion(bi *debug.BuildInfo, ok bool) string {
	if !ok || bi == nil {
		return ""
	}
	m := &bi.Main
	if m.Path != modulePath {
		m = nil
		for _, d := range bi.Deps {
			if d.Path == modulePath {
				m = d
				break
			}
		}
		if m == nil {
			return ""
		}
	}
	if m.Replace != nil {
		m = m.Replace
	}
	if m.Version == "(devel)" {
		return ""
	}
	return m.Version
}

// version is moduleVersion(debug.ReadBuildInfo()), read once.
var version = sync.OnceValue(func() string { return moduleVersion(debug.ReadBuildInfo()) })
