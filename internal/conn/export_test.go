package conn

import (
	"database/sql"
	"database/sql/driver"
)

// ColumnScan scans src into dst, a pointer to a field, through a colScanner and
// database/sql's conversion, as the scan of a wrapped destination does.
func ColumnScan(dst, src any) error {
	return sql.ConvertAssign(driver.ScanContext{}, &colScanner{dst: dst}, src)
}
