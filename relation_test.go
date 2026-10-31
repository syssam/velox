package velox

import (
	"testing"
	"unsafe"

	"github.com/syssam/velox2/internal/engine"
)

func TestHolderLayout(t *testing.T) {
	var o One[int]
	var m Many[int]
	var h engine.Holder
	if unsafe.Sizeof(o) != unsafe.Sizeof(h) || unsafe.Sizeof(m) != unsafe.Sizeof(h) ||
		unsafe.Offsetof(o.loaded) != unsafe.Offsetof(h.Loaded) || unsafe.Offsetof(m.loaded) != unsafe.Offsetof(h.Loaded) {
		t.Fatal("One and Many must have holder's layout")
	}
}
