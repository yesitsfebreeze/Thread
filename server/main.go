// go:build cgo
// Tiny Vaults MVP: Go + HTMX + SQLite(FTS5) — single file
// --------------------------------------------------------
// Features:
// - Create vaults
// - Define schemas (versioned per vault) with a tiny JSON schema
// - Create/search/edit documents; full‑text search via FTS5
// - No auth; single-user MVP
//
// Build:
//   go mod init vaults
//   go get github.com/mattn/go-sqlite3
//   go run .
// Visit: http://localhost:8080
//
// Notes:
// - Requires CGO for mattn/go-sqlite3. If you want pure-Go, swap to modernc.org/sqlite.
// - This is deliberately compact; extract into packages as you grow.

package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const PORT = "8080"

// --- Templates (embedded) ----------------------------------------------------

//go:embed templates/*
var tplFS embed.FS

var tpl = template.Must(template.ParseFS(tplFS, "templates/*.html"))

// --- Data types --------------------------------------------------------------

type vault struct {
	ID        int64
	Name      string
	Slug      string
	CreatedAt time.Time
}

type schema struct {
	ID        int64
	VaultID   int64
	Version   int
	Title     string
	JSON      string // raw json schema
	IsActive  bool
	CreatedAt time.Time
}

type document struct {
	ID        int64
	VaultID   int64
	SchemaID  int64
	Title     string
	DataJSON  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// minimal field metadata for dynamic forms
// choose simple types: string, number, boolean, text (textarea), select (enum)
type fieldDef struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // string|number|boolean|text|select
	Required bool     `json:"required"`
	Enum     []string `json:"enum,omitempty"`
}

type schemaDoc struct {
	Fields []fieldDef `json:"fields"`
}

// --- Globals ----------------------------------------------------------------

var db *sql.DB

// --- Main -------------------------------------------------------------------

func main() {
	var err error
	db, err = sql.Open("sqlite", "file:app.db")
	if err != nil {
		log.Fatal(err)
	}
	if err := initDB(db); err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/", handle_index)
	http.HandleFunc("/vaults", handle_create_vault)
	http.HandleFunc("/vault/", handle_vault_router)
	http.HandleFunc("/doc/", handle_doc_router)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(tplFS))))

	log.Println("listening on http://localhost:" + PORT)
	log.Fatal(http.ListenAndServe("localhost:"+PORT, nil))
}

// --- Routing helpers ---------------------------------------------------------

func handle_vault_router(w http.ResponseWriter, r *http.Request) {
	// routes:
	// GET  /vault/{id}
	// POST /vault/{id}/schemas
	// POST /vault/{id}/documents
	// GET  /vault/{id}/new (render form)
	// GET  /vault/{id}/search?q=...
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	idStr := parts[1]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 2 && r.Method == http.MethodGet {
		handle_vault_show(w, r, id)
		return
	}
	if len(parts) == 3 {
		switch parts[2] {
		case "schemas":
			if r.Method == http.MethodPost {
				handle_create_schema(w, r, id)
				return
			}
		case "documents":
			if r.Method == http.MethodPost {
				handle_create_document(w, r, id)
				return
			}
		case "new":
			if r.Method == http.MethodGet {
				handle_new_document_form(w, r, id)
				return
			}
		case "search":
			if r.Method == http.MethodGet {
				handle_search(w, r, id)
				return
			}
		}
	}
	http.NotFound(w, r)
}

func handle_doc_router(w http.ResponseWriter, r *http.Request) {
	// routes:
	// GET  /doc/{id}/edit
	// POST /doc/{id}/edit
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	idStr := parts[1]
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 3 && parts[2] == "edit" {
		switch r.Method {
		case http.MethodGet:
			handle_edit_document_form(w, r, id)
		case http.MethodPost:
			handle_update_document(w, r, id)
		default:
			http.Error(w, "method not allowed", 405)
		}
		return
	}
	http.NotFound(w, r)
}

// --- Handlers: index & vaults -----------------------------------------------

func handle_index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	rows, err := db.Query(`SELECT id, name, slug, created_at FROM vaults ORDER BY created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var items []vault
	for rows.Next() {
		var v vault
		if err := rows.Scan(&v.ID, &v.Name, &v.Slug, &v.CreatedAt); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		items = append(items, v)
	}
	data := map[string]any{"vaults": items}
	if err := tpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func handle_create_vault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.TrimSpace(r.FormValue("slug"))
	if name == "" || slug == "" {
		http.Error(w, "name and slug required", 400)
		return
	}
	res, err := db.Exec(`INSERT INTO vaults(name, slug, created_at) VALUES(?,?,?)`, name, slug, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	id, _ := res.LastInsertId()
	// HTMX support: if HX-Request, return the new row partial; else redirect
	if r.Header.Get("HX-Request") == "true" {
		var v = vault{ID: id, Name: name, Slug: slug, CreatedAt: time.Now().UTC()}
		_ = tpl.ExecuteTemplate(w, "_vault_row.html", v)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", id), 303)
}

func handle_vault_show(w http.ResponseWriter, r *http.Request, id int64) {
	var v vault
	err := db.QueryRow(`SELECT id, name, slug, created_at FROM vaults WHERE id=?`, id).Scan(&v.ID, &v.Name, &v.Slug, &v.CreatedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// active schema (if any)
	var s *schema
	row := db.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE vault_id=? AND is_active=1 ORDER BY version DESC LIMIT 1`, id)
	var tmp schema
	if err := row.Scan(&tmp.ID, &tmp.VaultID, &tmp.Version, &tmp.Title, &tmp.JSON, &tmp.IsActive, &tmp.CreatedAt); err == nil {
		s = &tmp
	}

	// recent documents
	rows, err := db.Query(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at FROM documents WHERE vault_id=? ORDER BY updated_at DESC LIMIT 50`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var docs []document
	for rows.Next() {
		var d document
		if err := rows.Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		docs = append(docs, d)
	}
	data := map[string]any{"vault": v, "schema": s, "documents": docs}
	if err := tpl.ExecuteTemplate(w, "vault.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// --- Handlers: schemas -------------------------------------------------------

func handle_create_schema(w http.ResponseWriter, r *http.Request, vaultID int64) {
	title := strings.TrimSpace(r.FormValue("title"))
	jsonStr := strings.TrimSpace(r.FormValue("json_schema"))
	if title == "" || jsonStr == "" {
		http.Error(w, "title and json_schema required", 400)
		return
	}
	// validate schema JSON
	var sd schemaDoc
	if err := json.Unmarshal([]byte(jsonStr), &sd); err != nil {
		http.Error(w, "invalid json_schema: "+err.Error(), 400)
		return
	}
	if err := validate_schema(sd); err != nil {
		http.Error(w, "invalid schema: "+err.Error(), 400)
		return
	}
	// version = max(version)+1
	var version int
	_ = db.QueryRow(`SELECT COALESCE(MAX(version),0)+1 FROM schemas WHERE vault_id=?`, vaultID).Scan(&version)
	// deactivate others, insert new active
	tx, _ := db.Begin()
	_, _ = tx.Exec(`UPDATE schemas SET is_active=0 WHERE vault_id=?`, vaultID)
	res, err := tx.Exec(`INSERT INTO schemas(vault_id, version, title, json, is_active, created_at) VALUES(?,?,?,?,1,?)`, vaultID, version, title, jsonStr, time.Now().UTC())
	if err != nil {
		_ = tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	_, _ = res.LastInsertId()
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/vault/%d", vaultID))
		w.WriteHeader(204)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", vaultID), 303)
}

func validate_schema(sd schemaDoc) error {
	if len(sd.Fields) == 0 {
		return errors.New("fields[] required")
	}
	for i, f := range sd.Fields {
		if f.Name == "" {
			return fmt.Errorf("fields[%d].name required", i)
		}
		if f.Label == "" {
			return fmt.Errorf("fields[%d].label required", i)
		}
		switch f.Type {
		case "string", "number", "boolean", "text", "select":
		default:
			return fmt.Errorf("fields[%d].type invalid", i)
		}
		if f.Type == "select" && len(f.Enum) == 0 {
			return fmt.Errorf("fields[%d].enum required for select", i)
		}
	}
	return nil
}

// --- Handlers: documents -----------------------------------------------------

func handle_new_document_form(w http.ResponseWriter, r *http.Request, vaultID int64) {
	var s schema
	err := db.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE vault_id=? AND is_active=1 ORDER BY version DESC LIMIT 1`, vaultID).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt)
	if err != nil {
		http.Error(w, "no active schema", 400)
		return
	}
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)
	data := map[string]any{"vault_id": vaultID, "schema": s, "sd": sd}
	_ = tpl.ExecuteTemplate(w, "doc_new.html", data)
}

func handle_create_document(w http.ResponseWriter, r *http.Request, vaultID int64) {
	// load active schema
	var s schema
	err := db.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE vault_id=? AND is_active=1 ORDER BY version DESC LIMIT 1`, vaultID).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt)
	if err != nil {
		http.Error(w, "no active schema", 400)
		return
	}
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)

	// build data map from form values
	m := map[string]any{}
	var title string = strings.TrimSpace(r.FormValue("__title"))
	for _, f := range sd.Fields {
		val := strings.TrimSpace(r.FormValue(f.Name))
		if f.Required && val == "" {
			http.Error(w, fmt.Sprintf("%s required", f.Label), 400)
			return
		}
		switch f.Type {
		case "number":
			if val == "" {
				m[f.Name] = nil
			} else {
				if _, err := strconv.ParseFloat(val, 64); err != nil {
					http.Error(w, f.Label+": must be number", 400)
					return
				}
				m[f.Name] = val
			}
		case "boolean":
			m[f.Name] = (r.FormValue(f.Name) == "on" || r.FormValue(f.Name) == "true")
		case "select":
			if val != "" && !contains(f.Enum, val) {
				http.Error(w, f.Label+": invalid option", 400)
				return
			}
			m[f.Name] = val
		default: // string|text
			m[f.Name] = val
		}
	}
	bj, _ := json.Marshal(m)
	if title == "" {
		title = derive_title(m)
	}

	tx, _ := db.Begin()
	res, err := tx.Exec(`INSERT INTO documents(vault_id, schema_id, title, data_json, created_at, updated_at) VALUES(?,?,?,?,?,?)`, vaultID, s.ID, title, string(bj), time.Now().UTC(), time.Now().UTC())
	if err != nil {
		_ = tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	docID, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO document_fts(rowid, title, data_text) VALUES(?,?,?)`, docID, title, flatten_json_for_fts(m)); err != nil {
		_ = tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/vault/%d", vaultID))
		w.WriteHeader(204)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", vaultID), 303)
}

func handle_edit_document_form(w http.ResponseWriter, r *http.Request, docID int64) {
	var d document
	err := db.QueryRow(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at FROM documents WHERE id=?`, docID).
		Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var s schema
	_ = db.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE id=?`, d.SchemaID).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt)
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)
	var data map[string]any
	_ = json.Unmarshal([]byte(d.DataJSON), &data)
	payload := map[string]any{"doc": d, "schema": s, "sd": sd, "data": data}
	_ = tpl.ExecuteTemplate(w, "doc_edit.html", payload)
}

func handle_update_document(w http.ResponseWriter, r *http.Request, docID int64) {
	var d document
	err := db.QueryRow(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at FROM documents WHERE id=?`, docID).
		Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var s schema
	_ = db.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE id=?`, d.SchemaID).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt)
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)

	m := map[string]any{}
	title := strings.TrimSpace(r.FormValue("__title"))
	if title == "" {
		title = d.Title
	}
	for _, f := range sd.Fields {
		val := strings.TrimSpace(r.FormValue(f.Name))
		if f.Required && val == "" {
			http.Error(w, fmt.Sprintf("%s required", f.Label), 400)
			return
		}
		switch f.Type {
		case "number":
			if val == "" {
				m[f.Name] = nil
			} else {
				if _, err := strconv.ParseFloat(val, 64); err != nil {
					http.Error(w, f.Label+": must be number", 400)
					return
				}
				m[f.Name] = val
			}
		case "boolean":
			m[f.Name] = (r.FormValue(f.Name) == "on" || r.FormValue(f.Name) == "true")
		case "select":
			var fdef fieldDef
			for _, fd := range sd.Fields {
				if fd.Name == f.Name {
					fdef = fd
					break
				}
			}
			if val != "" && !contains(fdef.Enum, val) {
				http.Error(w, f.Label+": invalid option", 400)
				return
			}
			m[f.Name] = val
		default:
			m[f.Name] = val
		}
	}
	bj, _ := json.Marshal(m)
	_, err = db.Exec(`UPDATE documents SET title=?, data_json=?, updated_at=? WHERE id=?`, title, string(bj), time.Now().UTC(), docID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_, _ = db.Exec(`INSERT INTO document_fts(document_fts, rowid, title, data_text) VALUES('delete', ?, '', '')`, docID)
	_, _ = db.Exec(`INSERT INTO document_fts(rowid, title, data_text) VALUES(?,?,?)`, docID, title, flatten_json_for_fts(m))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/vault/%d", d.VaultID))
		w.WriteHeader(204)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", d.VaultID), 303)
}

func handle_search(w http.ResponseWriter, r *http.Request, vaultID int64) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := db.Query(`
	SELECT d.id, d.vault_id, d.schema_id, d.title, d.data_json, d.created_at, d.updated_at
	FROM document_fts f JOIN documents d ON d.id=f.rowid
	WHERE d.vault_id = ? AND document_fts MATCH ?
	ORDER BY rank
	LIMIT 100`, vaultID, q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var docs []document
	for rows.Next() {
		var d document
		if err := rows.Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		docs = append(docs, d)
	}
	if r.Header.Get("HX-Request") == "true" {
		_ = tpl.ExecuteTemplate(w, "_doc_list.html", docs)
		return
	}
	// fallback (non-htmx) — render the vault page with results replacing list
	var v vault
	_ = db.QueryRow(`SELECT id, name, slug, created_at FROM vaults WHERE id=?`, vaultID).Scan(&v.ID, &v.Name, &v.Slug, &v.CreatedAt)
	data := map[string]any{"vault": v, "schema": nil, "documents": docs}
	_ = tpl.ExecuteTemplate(w, "vault.html", data)
}

// --- DB init -----------------------------------------------------------------

func initDB(db *sql.DB) error {
	stmts := []string{
		`PRAGMA foreign_keys=ON;`,
		`CREATE TABLE IF NOT EXISTS vaults (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS schemas (
			id INTEGER PRIMARY KEY,
			vault_id INTEGER NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			title TEXT NOT NULL,
			json TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_schemas_vault_version ON schemas(vault_id, version);`,
		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY,
			vault_id INTEGER NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
			schema_id INTEGER NOT NULL REFERENCES schemas(id),
			title TEXT NOT NULL,
			data_json TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS document_fts USING fts5(title, data_text, content='documents', content_rowid='id');`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

// --- Utils -------------------------------------------------------------------

func contains(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

func derive_title(m map[string]any) string {
	// Pick first non-empty string field as title; fallback to timestamp
	for k, v := range m {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if b, ok := v.(bool); ok {
			return fmt.Sprintf("%s:%v", k, b)
		}
	}
	return time.Now().Format(time.RFC3339)
}

func flatten_json_for_fts(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		fmt.Fprintf(&b, "%s: %v\n", k, v)
	}
	return b.String()
}
