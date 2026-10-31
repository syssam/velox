package load

import (
	"reflect"
	"testing"

	"github.com/syssam/velox/schema/mixin"
)

func TestMixinPkg(t *testing.T) {
	if want := reflect.TypeFor[mixin.Time]().PkgPath() + "."; mixinPkg != want {
		t.Errorf("mixinPkg is %q, want %q", mixinPkg, want)
	}
}
