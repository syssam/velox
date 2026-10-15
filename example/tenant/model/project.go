// Package model holds the tenant example's entity. project_velox.go beside
// this file is written by go tool velox-gen.
package model

import "github.com/syssam/velox2"

// Project belongs to one organization, the tenant. Its name is unique within
// the organization, not across organizations.
type Project struct {
	ID     int64
	OrgID  int64
	Name   string
	Status string
}

// Schema declares the tenant column: every statement on Project is scoped to
// the context's tenant, and fails without one.
func (x *Project) Schema(s *velox.Schema) {
	s.Table("projects")
	s.Tenant(&x.OrgID)
	s.Unique(&x.OrgID, &x.Name)
}
