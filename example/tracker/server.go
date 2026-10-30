package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/example/tracker/gen"
)

const (
	maxBody      = 1 << 20
	defaultLimit = 20
	maxLimit     = 100
	maxLabels    = 20
)

type server struct{ db *velox.DB }

// newServer returns the API's routes.
func newServer(db *velox.DB) http.Handler {
	s := &server{db: db}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /signup", s.signup)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.authed(s.logout))
	mux.HandleFunc("GET /me", s.authed(s.me))
	mux.HandleFunc("POST /users", s.authed(s.invite))
	mux.HandleFunc("GET /projects", s.authed(s.listProjects))
	mux.HandleFunc("POST /projects", s.authed(s.createProject))
	mux.HandleFunc("GET /projects/{id}/issues", s.authed(s.listIssues))
	mux.HandleFunc("POST /projects/{id}/issues", s.authed(s.createIssue))
	mux.HandleFunc("GET /issues/{id}", s.authed(s.getIssue))
	mux.HandleFunc("PATCH /issues/{id}", s.authed(s.updateIssue))
	mux.HandleFunc("DELETE /issues/{id}", s.authed(s.deleteIssue))
	mux.HandleFunc("POST /issues/{id}/comments", s.authed(s.addComment))
	return mux
}

// projectView is a Project with the count of its open Issues.
type projectView struct {
	*gen.Project
	OpenIssues int64 `json:"open_issues"`
}

// listProjects returns a page of projects. CountEach counts every project's open
// issues in one statement, however many projects there are.
func (s *server) listProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	after, limit, ok := page(w, r)
	if !ok {
		return
	}
	P, I := gen.Projects, gen.Issues
	rows, err := s.db.Query[gen.Project]().Where(P.ID.GT(after)).OrderBy(P.ID.Asc()).Limit(limit + 1).All(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, next := cut(rows, limit, func(p *gen.Project) int64 { return p.ID })
	owners := make([]*gen.Project, len(rows))
	for i := range rows {
		owners[i] = &rows[i]
	}
	open := s.db.Query[gen.Issue]().Where(I.Status.EQ(gen.IssueStatusOpen))
	counts, err := velox.CountEach(ctx, open, P.Issues, owners)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	views := make([]projectView, len(rows))
	for i := range rows {
		views[i] = projectView{owners[i], counts[i]}
	}
	writeJSON(w, http.StatusOK, pageOf(views, next))
}

func (s *server) createProject(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if !readJSON(w, r, &in) {
		return
	}
	p := gen.Project{Name: in.Name}
	if err := s.db.Insert(&p).Exec(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, &p)
}

// listIssues returns a page of a project's issues, by id, with their labels and
// authors, filtered by ?status= and ?label=. Keyset paging costs the same on the
// last page as on the first, and a row inserted meanwhile does not shift it.
func (s *server) listIssues(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	project, ok := pathID(w, r)
	if !ok {
		return
	}
	after, limit, ok := page(w, r)
	if !ok {
		return
	}
	I, L := gen.Issues, gen.Labels
	if exists, err := s.db.Query[gen.Project]().Where(gen.Projects.ID.EQ(project)).Exists(ctx); err != nil {
		s.fail(w, r, err)
		return
	} else if !exists {
		writeJSON(w, http.StatusNotFound, errorBody{"no such project"})
		return
	}
	q := s.db.Query[gen.Issue]().Where(I.ProjectID.EQ(project), I.ID.GT(after))
	if v := r.URL.Query().Get("status"); v != "" {
		st := gen.IssueStatus(v)
		if !st.IsValid() {
			writeJSON(w, http.StatusUnprocessableEntity, errorBody{fmt.Sprintf("no status %q", v)})
			return
		}
		q = q.Where(I.Status.EQ(st))
	}
	if v := r.URL.Query().Get("label"); v != "" {
		q = q.Where(I.Labels.HasWhere(L.Name.EQ(v)))
	}
	rows, err := q.With(I.Labels.OrderBy(L.Name.Asc())).With(I.Author).OrderBy(I.ID.Asc()).Limit(limit + 1).All(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, next := cut(rows, limit, func(x *gen.Issue) int64 { return x.ID })
	writeJSON(w, http.StatusOK, pageOf(rows, next))
}

// createIssue inserts an issue and its labels in one transaction: a label that
// fails validation leaves no issue behind. A label is created the first time its
// name is used, and the upsert is safe against a concurrent first use.
func (s *server) createIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Title, Body string
		Labels      []string
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.Labels) > maxLabels {
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{fmt.Sprintf("an issue has at most %d labels", maxLabels)})
		return
	}
	p, _ := gen.PrincipalFromContext(r.Context())
	issue := gen.Issue{ProjectID: project, AuthorID: p.UserID, Title: in.Title, Body: in.Body}
	err := s.db.Tx(r.Context(), func(ctx context.Context, tx *velox.DB) error {
		exists, err := tx.Query[gen.Project]().Where(gen.Projects.ID.EQ(project)).Exists(ctx)
		if err != nil {
			return err
		}
		if !exists {
			return velox.ErrNotFound
		}
		if err := tx.Insert(&issue).Exec(ctx); err != nil {
			return err
		}
		return setLabels(ctx, tx, issue.ID, in.Labels)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, &issue)
}

// setLabels links an issue to the labels of these names, creating those that do
// not exist yet. Sorting the names makes two transactions take their locks in one
// order: in the order of the request, ["a","b"] and ["b","a"] could deadlock on
// PostgreSQL. A repeated name is one label.
func setLabels(ctx context.Context, tx *velox.DB, issue int64, names []string) error {
	if len(names) == 0 {
		return nil
	}
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	L := gen.Labels
	rows := make([]gen.Label, len(names))
	for i, n := range names {
		rows[i].Name = n
	}
	if err := tx.InsertAll(rows).OnConflict(L.OrgID, L.Name).DoNothing().Exec(ctx); err != nil {
		return err
	}
	labels, err := tx.Query[gen.Label]().Where(L.Name.In(names...)).Limit(len(names)).All(ctx)
	if err != nil {
		return err
	}
	links := make([]gen.IssueLabel, len(labels))
	for i := range labels {
		links[i] = gen.IssueLabel{IssueID: issue, LabelID: labels[i].ID}
	}
	return tx.InsertAll(links).Exec(ctx)
}

// getIssue returns an issue with all it relates to, in a fixed number of statements,
// however many comments there are.
func (s *server) getIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	I, C, L := gen.Issues, gen.Comments, gen.Labels
	x, err := s.db.Query[gen.Issue]().
		Where(I.ID.EQ(id)).
		With(I.Author).
		With(I.Assignee).
		With(I.Labels.OrderBy(L.Name.Asc())).
		With(I.Comments.OrderBy(C.ID.Asc()).With(C.Author)).
		One(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, x)
}

// updateIssue changes the fields the body names. assignee_id null unassigns. An
// assignee must be a user of the caller's organization: the foreign key alone would
// accept another organization's.
func (s *server) updateIssue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Title, Body, Status *string
		AssigneeID          json.RawMessage `json:"assignee_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	I := gen.Issues
	var set []velox.Setter[gen.Issue]
	if in.Title != nil {
		set = append(set, I.Title.Set(*in.Title))
	}
	if in.Body != nil {
		set = append(set, I.Body.Set(*in.Body))
	}
	if in.Status != nil {
		st := gen.IssueStatus(*in.Status)
		if !st.IsValid() {
			writeJSON(w, http.StatusUnprocessableEntity, errorBody{fmt.Sprintf("no status %q", *in.Status)})
			return
		}
		set = append(set, I.Status.Set(st))
	}
	if len(in.AssigneeID) > 0 {
		if bytes.Equal(in.AssigneeID, []byte("null")) {
			set = append(set, I.AssigneeID.SetNull())
		} else {
			var who int64
			if err := json.Unmarshal(in.AssigneeID, &who); err != nil {
				writeJSON(w, http.StatusBadRequest, errorBody{"assignee_id is a number or null"})
				return
			}
			exists, err := s.db.Query[gen.User]().Where(gen.Users.ID.EQ(who)).Exists(ctx)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if !exists {
				writeJSON(w, http.StatusUnprocessableEntity, errorBody{"no such assignee"})
				return
			}
			set = append(set, I.AssigneeID.Set(who))
		}
	}
	if len(set) == 0 {
		writeJSON(w, http.StatusBadRequest, errorBody{"nothing to change"})
		return
	}
	// The policy is part of the statement, so no row changed means either no such
	// issue or one the caller may not change. The second read tells them apart.
	n, err := s.db.Update[gen.Issue]().Set(set...).Where(I.ID.EQ(id)).Exec(ctx)
	if err == nil && n == 0 {
		err = s.missingOrDenied(ctx, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	x, err := s.db.Query[gen.Issue]().Where(I.ID.EQ(id)).One(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, x)
}

// deleteIssue deletes an issue. The foreign keys delete its comments and label
// links with it, whoever wrote them; the policy decides only on the issue.
func (s *server) deleteIssue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	n, err := s.db.Delete[gen.Issue]().Where(gen.Issues.ID.EQ(id)).Exec(ctx)
	if err == nil && n == 0 {
		err = s.missingOrDenied(ctx, id)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// missingOrDenied is the error of a write that changed no row of issue id.
func (s *server) missingOrDenied(ctx context.Context, id int64) error {
	exists, err := s.db.Query[gen.Issue]().Where(gen.Issues.ID.EQ(id)).Exists(ctx)
	switch {
	case err != nil:
		return err
	case exists:
		return fmt.Errorf("issue %d: %w", id, velox.ErrDenied)
	}
	return velox.ErrNotFound
}

func (s *server) addComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct{ Body string }
	if !readJSON(w, r, &in) {
		return
	}
	// The issue is read in the caller's tenant: the foreign key alone would accept
	// an issue of another organization.
	exists, err := s.db.Query[gen.Issue]().Where(gen.Issues.ID.EQ(id)).Exists(ctx)
	if err == nil && !exists {
		err = velox.ErrNotFound
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, _ := gen.PrincipalFromContext(ctx)
	c := gen.Comment{IssueID: id, AuthorID: p.UserID, Body: in.Body}
	if err := s.db.Insert(&c).Exec(ctx); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, &c)
}

// fail answers err with its HTTP status. What the database said stays in the log:
// the client gets a message that names no table or constraint.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *velox.ValidationError
	var ce *velox.ConstraintError
	switch {
	case velox.IsNotFound(err):
		writeJSON(w, http.StatusNotFound, errorBody{"not found"})
	case errors.Is(err, velox.ErrDenied):
		writeJSON(w, http.StatusForbidden, errorBody{"not allowed"})
	case errors.As(err, &ve):
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{ve.Field + ": " + ve.Err.Error()})
	case errors.As(err, &ce) && ce.Kind == velox.ConstraintUnique:
		writeJSON(w, http.StatusConflict, errorBody{"that already exists"})
	case errors.As(err, &ce) && ce.Kind == velox.ConstraintForeignKey:
		writeJSON(w, http.StatusUnprocessableEntity, errorBody{"it refers to something that does not exist"})
	case errors.Is(err, context.Canceled):
		// The client went away; nobody reads this.
	default:
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
		writeJSON(w, http.StatusInternalServerError, errorBody{"internal error"})
	}
}

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write: %v", err)
	}
}

// readJSON decodes the body, one JSON value of v's fields, into v, or answers the
// request and returns false.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	d.DisallowUnknownFields()
	err := d.Decode(v)
	if err == nil {
		if _, more := d.Token(); !errors.Is(more, io.EOF) {
			err = errors.New("data after the JSON value")
		}
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorBody{"the body is too large"})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, errorBody{"the body is not the JSON expected"})
	}
	return err == nil
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorBody{"not found"})
		return 0, false
	}
	return id, true
}

// page reads ?after= and ?limit=.
func page(w http.ResponseWriter, r *http.Request) (after int64, limit int, ok bool) {
	limit = defaultLimit
	var err error
	if v := r.URL.Query().Get("after"); v != "" {
		if after, err = strconv.ParseInt(v, 10, 64); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{"after is a number"})
			return 0, 0, false
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > maxLimit {
			writeJSON(w, http.StatusBadRequest, errorBody{fmt.Sprintf("limit is from 1 to %d", maxLimit)})
			return 0, 0, false
		}
	}
	return after, limit, true
}

// cut trims rows, read with one more than limit, to limit, and returns the cursor
// of the next page: the last row's key, or nil when there is no next page.
func cut[E any](rows []E, limit int, key func(*E) int64) ([]E, *int64) {
	if len(rows) <= limit {
		return rows, nil
	}
	rows = rows[:limit]
	next := key(&rows[limit-1])
	return rows, &next
}

type pageBody[E any] struct {
	Items []E    `json:"items"`
	Next  *int64 `json:"next"`
}

func pageOf[E any](rows []E, next *int64) pageBody[E] {
	if rows == nil {
		rows = []E{}
	}
	return pageBody[E]{rows, next}
}
