package gqlgraph

import (
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// fakeCursorType is the type of the cursors ConnectionFromRows gives.
const fakeCursorType = "ConnectionFromRows"

// ConnectionFromRows returns the connection whose page is rows, whose page
// info is info and whose totalCount is total, with no DB and no query: its
// Page and TotalCount read nothing. It is for a test's fake of code that
// returns a generated connection, as ent's plain connection struct is
// built by hand:
//
//	conn := &veloxgen.PostConnection{Conn: gqlgraph.ConnectionFromRows(posts, 2, veloxgql.PageInfo{})}
//
// Each row's cursor has the type "ConnectionFromRows" and the row's index
// as its key: it encodes, and no Paginate takes it. info's StartCursor and
// EndCursor, when nil, are the first and the last row's cursors. A nil row
// is kept, as the page's node.
func ConnectionFromRows[E any](rows []*E, total int, info veloxgql.PageInfo) *Connection {
	p := &Page{Rows: make([]any, len(rows)), Cursors: make([]veloxgql.Cursor, len(rows)), Info: info}
	for i, r := range rows {
		p.Rows[i] = r
		p.Cursors[i] = veloxgql.Cursor{Type: fakeCursorType, Key: int64(i)}
	}
	if n := len(rows); n > 0 {
		if p.Info.StartCursor == nil {
			p.Info.StartCursor = &p.Cursors[0]
		}
		if p.Info.EndCursor == nil {
			p.Info.EndCursor = &p.Cursors[n-1]
		}
	}
	c := &Connection{typ: fakeCursorType, page: p, count: total}
	c.pageOnce.Do(func() {})
	c.countOnce.Do(func() {})
	return c
}
