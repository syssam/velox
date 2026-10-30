package apitest

import (
	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/apitest/fixture"
)

var dBookTags = struct {
	BookID velox.NumberCol[fixture.DBookTag, int64]
	TagID  velox.NumberCol[fixture.DBookTag, int64]
}{velox.NumberCol[fixture.DBookTag, int64]{I: 0}, velox.NumberCol[fixture.DBookTag, int64]{I: 1}}
