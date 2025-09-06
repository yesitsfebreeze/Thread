package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func register_routes() {
	http.HandleFunc("/", handle_index)
	http.HandleFunc("/vaults", handle_create_vault) // POST /vaults
	http.HandleFunc("/vault/", handle_vault_router) // subroutes under /vault/{id}
}

// GET /  -> list vaults (json only)
func handle_index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !must_method(w, r, http.MethodGet) {
		return
	}
	items, err := ix.list_vaults()
	if err != nil {
		json_err(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	with_json(w, map[string]any{
		"vaults": items,
		"count":  len(items),
	})
}

// POST /vaults {name, slug} -> create vault
func handle_create_vault(w http.ResponseWriter, r *http.Request) {
	if !must_method(w, r, http.MethodPost) {
		return
	}
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		json_err(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Slug) == "" {
		json_err(w, http.StatusBadRequest, "missing_fields", "name and slug are required")
		return
	}
	v, err := ix.create_vault(strings.TrimSpace(in.Name), strings.TrimSpace(in.Slug))
	if err != nil {
		json_err(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	json_created(w, v)
}

// /vault/{id}
//
//	GET                     -> show vault summary (vault, active_schema?, recent docs)
//	POST /schemas           -> create schema
//	POST /documents         -> create document
//	GET  /search?q=...      -> search docs (fts)
//	GET  /doc/{docID}       -> get doc
//	PUT  /doc/{docID}       -> update doc
func handle_vault_router(w http.ResponseWriter, r *http.Request) {
	pp := path_parts(r)
	// expect at least ["vault", "{id}"]
	if len(pp) < 2 {
		http.NotFound(w, r)
		return
	}
	// pp[0] == "vault"
	if pp[0] != "vault" {
		http.NotFound(w, r)
		return
	}

	vID, err := parse_int64(pp[1])
	if err != nil {
		json_err(w, http.StatusBadRequest, "invalid_id", "vault id must be an integer")
		return
	}

	// /vault/{id}
	if len(pp) == 2 {
		switch r.Method {
		case http.MethodGet:
			handle_vault_show(w, r, vID)
			return
		default:
			must_method(w, r, http.MethodGet)
			return
		}
	}

	// /vault/{id}/schemas | /documents | /search | /doc/{docID}
	switch pp[2] {
	case "schemas":
		if len(pp) != 3 {
			http.NotFound(w, r)
			return
		}
		if !must_method(w, r, http.MethodPost) {
			return
		}
		handle_create_schema(w, r, vID)
		return

	case "documents":
		if len(pp) != 3 {
			http.NotFound(w, r)
			return
		}
		if !must_method(w, r, http.MethodPost) {
			return
		}
		handle_create_document(w, r, vID)
		return

	case "search":
		if len(pp) != 3 {
			http.NotFound(w, r)
			return
		}
		if !must_method(w, r, http.MethodGet) {
			return
		}
		handle_search(w, r, vID)
		return

	case "doc":
		if len(pp) != 4 {
			http.NotFound(w, r)
			return
		}
		docID, err := parse_int64(pp[3])
		if err != nil {
			json_err(w, http.StatusBadRequest, "invalid_id", "doc id must be an integer")
			return
		}
		switch r.Method {
		case http.MethodGet:
			handle_get_document(w, r, vID, docID)
			return
		case http.MethodPut, http.MethodPatch:
			handle_update_document(w, r, vID, docID)
			return
		default:
			must_method(w, r, http.MethodGet, http.MethodPut, http.MethodPatch)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
}

// GET /vault/{id}
func handle_vault_show(w http.ResponseWriter, r *http.Request, vaultID int64) {
	// parse slug instead of int
	parts := path_parts(r)
	slug := parts[1] // e.g. "harbor"

	// look up vault by slug if you need meta info
	v, err := ix.get_vault_by_slug(slug)

	// pass slug into per-vault db call
	vdb, err := vs.db_for_vault(slug)

	// active schema (optional)
	var s *schema
	var tmp schema
	if err := vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at
	                         FROM schemas WHERE is_active=1
	                         ORDER BY version DESC LIMIT 1`).
		Scan(&tmp.ID, &tmp.VaultID, &tmp.Version, &tmp.Title, &tmp.JSON, &tmp.IsActive, &tmp.CreatedAt); err == nil {
		s = &tmp
	}

	// recent docs
	rows, err := vdb.Query(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at
	                        FROM documents ORDER BY updated_at DESC LIMIT 50`)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	defer rows.Close()

	var docs []document
	for rows.Next() {
		var d document
		if err := rows.Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
			json_err(w, http.StatusInternalServerError, "scan_failed", err.Error())
			return
		}
		docs = append(docs, d)
	}

	with_json(w, map[string]any{
		"vault":     v,
		"schema":    s,    // may be null
		"documents": docs, // may be empty
	})
}

// POST /vault/{id}/schemas {title, json_schema}
func handle_create_schema(w http.ResponseWriter, r *http.Request, vaultID int64) {
	var in struct {
		Title      string          `json:"title"`
		JSONSchema json.RawMessage `json:"json_schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		json_err(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if strings.TrimSpace(in.Title) == "" || len(in.JSONSchema) == 0 {
		json_err(w, http.StatusBadRequest, "missing_fields", "title and json_schema are required")
		return
	}

	var sd schemaDoc
	if err := json.Unmarshal(in.JSONSchema, &sd); err != nil {
		json_err(w, http.StatusBadRequest, "invalid_schema_json", err.Error())
		return
	}
	if err := validate_schema(sd); err != nil {
		json_err(w, http.StatusBadRequest, "invalid_schema", err.Error())
		return
	}

	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "db_open_failed", err.Error())
		return
	}

	var version int
	_ = vdb.QueryRow(`SELECT COALESCE(MAX(version),0)+1 FROM schemas`).Scan(&version)

	tx, _ := vdb.Begin()
	_, _ = tx.Exec(`UPDATE schemas SET is_active=0`)
	_, err = tx.Exec(`INSERT INTO schemas(vault_id, version, title, json, is_active, created_at)
	                  VALUES(?,?,?,?,1,?)`, vaultID, version, in.Title, string(in.JSONSchema), time.Now().UTC())
	if err != nil {
		_ = tx.Rollback()
		json_err(w, http.StatusInternalServerError, "insert_failed", err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		json_err(w, http.StatusInternalServerError, "commit_failed", err.Error())
		return
	}

	json_created(w, map[string]any{
		"vault_id":  vaultID,
		"version":   version,
		"title":     in.Title,
		"json":      json.RawMessage(in.JSONSchema),
		"is_active": true,
	})
}

// POST /vault/{id}/documents
// body: { "title": "optional", "data": {<fields>}}
func handle_create_document(w http.ResponseWriter, r *http.Request, vaultID int64) {
	type input struct {
		Title string         `json:"title"`
		Data  map[string]any `json:"data"`
	}
	var in input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		json_err(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "db_open_failed", err.Error())
		return
	}

	var s schema
	if err := vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at
	                         FROM schemas WHERE is_active=1 ORDER BY version DESC LIMIT 1`).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt); err != nil {
		json_err(w, http.StatusBadRequest, "no_active_schema", "create a schema first")
		return
	}
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)

	// validate / coerce against schema definition (basic)
	m := map[string]any{}
	for _, f := range sd.Fields {
		val, ok := in.Data[f.Name]
		if f.Required && (!ok || val == "") {
			json_err(w, http.StatusBadRequest, "field_required", f.Label+" required")
			return
		}
		switch f.Type {
		case "number":
			switch v := val.(type) {
			case float64, int, int64, json.Number:
				m[f.Name] = v
			case string:
				if v == "" {
					m[f.Name] = nil
				} else {
					if _, err := strconv.ParseFloat(v, 64); err != nil {
						json_err(w, http.StatusBadRequest, "type_error", f.Label+": must be number")
						return
					}
					m[f.Name] = v
				}
			case nil:
				m[f.Name] = nil
			default:
				json_err(w, http.StatusBadRequest, "type_error", f.Label+": must be number")
				return
			}
		case "boolean":
			switch v := val.(type) {
			case bool:
				m[f.Name] = v
			case string:
				m[f.Name] = (strings.ToLower(v) == "true" || strings.ToLower(v) == "on" || v == "1")
			default:
				m[f.Name] = false
			}
		case "select":
			sval := fmt.Sprint(val)
			if sval != "" && !contains(f.Enum, sval) {
				json_err(w, http.StatusBadRequest, "invalid_option", f.Label+": invalid option")
				return
			}
			m[f.Name] = sval
		default:
			// string or whatever
			m[f.Name] = fmt.Sprint(val)
		}
	}
	bj, _ := json.Marshal(m)

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = derive_title(m)
	}

	tx, _ := vdb.Begin()
	res, err := tx.Exec(`INSERT INTO documents(vault_id, schema_id, title, data_json, created_at, updated_at)
	                     VALUES(?,?,?,?,?,?)`, vaultID, s.ID, title, string(bj), time.Now().UTC(), time.Now().UTC())
	if err != nil {
		_ = tx.Rollback()
		json_err(w, http.StatusInternalServerError, "insert_failed", err.Error())
		return
	}
	docID, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO document_fts(rowid, title, data_text) VALUES(?,?,?)`,
		docID, title, flatten_json_for_fts(m)); err != nil {
		_ = tx.Rollback()
		json_err(w, http.StatusInternalServerError, "fts_failed", err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		json_err(w, http.StatusInternalServerError, "commit_failed", err.Error())
		return
	}

	json_created(w, map[string]any{
		"id":         docID,
		"vault_id":   vaultID,
		"schema_id":  s.ID,
		"title":      title,
		"data_json":  json.RawMessage(bj),
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
	})
}

// GET /vault/{id}/doc/{docID}
func handle_get_document(w http.ResponseWriter, r *http.Request, vaultID, docID int64) {
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "db_open_failed", err.Error())
		return
	}
	var d document
	if err := vdb.QueryRow(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at
	                        FROM documents WHERE id=?`, docID).
		Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
		json_err(w, http.StatusNotFound, "doc_not_found", "document does not exist")
		return
	}
	with_json(w, d)
}

// PUT/PATCH /vault/{id}/doc/{docID}
func handle_update_document(w http.ResponseWriter, r *http.Request, vaultID, docID int64) {
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "db_open_failed", err.Error())
		return
	}

	var d document
	if err := vdb.QueryRow(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at
	                        FROM documents WHERE id=?`, docID).
		Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
		json_err(w, http.StatusNotFound, "doc_not_found", "document does not exist")
		return
	}

	var s schema
	_ = vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE id=?`, d.SchemaID).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt)

	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)

	type input struct {
		Title string         `json:"title"`
		Data  map[string]any `json:"data"`
	}
	var in input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		json_err(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	m := map[string]any{}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = d.Title
	}
	for _, f := range sd.Fields {
		val, ok := in.Data[f.Name]
		if f.Required && (!ok || val == "") {
			json_err(w, http.StatusBadRequest, "field_required", f.Label+" required")
			return
		}
		switch f.Type {
		case "number":
			switch v := val.(type) {
			case float64, int, int64, json.Number:
				m[f.Name] = v
			case string:
				if v == "" {
					m[f.Name] = nil
				} else {
					if _, err := strconv.ParseFloat(v, 64); err != nil {
						json_err(w, http.StatusBadRequest, "type_error", f.Label+": must be number")
						return
					}
					m[f.Name] = v
				}
			case nil:
				m[f.Name] = nil
			default:
				json_err(w, http.StatusBadRequest, "type_error", f.Label+": must be number")
				return
			}
		case "boolean":
			switch v := val.(type) {
			case bool:
				m[f.Name] = v
			case string:
				m[f.Name] = (strings.ToLower(v) == "true" || strings.ToLower(v) == "on" || v == "1")
			default:
				m[f.Name] = false
			}
		case "select":
			sval := fmt.Sprint(val)
			var fdef fieldDef
			for _, fd := range sd.Fields {
				if fd.Name == f.Name {
					fdef = fd
					break
				}
			}
			if sval != "" && !contains(fdef.Enum, sval) {
				json_err(w, http.StatusBadRequest, "invalid_option", f.Label+": invalid option")
				return
			}
			m[f.Name] = sval
		default:
			m[f.Name] = fmt.Sprint(val)
		}
	}
	bj, _ := json.Marshal(m)

	if _, err := vdb.Exec(`UPDATE documents SET title=?, data_json=?, updated_at=? WHERE id=?`,
		title, string(bj), time.Now().UTC(), docID); err != nil {
		json_err(w, http.StatusInternalServerError, "update_failed", err.Error())
		return
	}
	_, _ = vdb.Exec(`INSERT INTO document_fts(document_fts, rowid, title, data_text) VALUES('delete', ?, '', '')`, docID)
	_, _ = vdb.Exec(`INSERT INTO document_fts(rowid, title, data_text) VALUES(?,?,?)`, docID, title, flatten_json_for_fts(m))

	with_json(w, map[string]any{
		"id":         docID,
		"vault_id":   vaultID,
		"schema_id":  d.SchemaID,
		"title":      title,
		"data_json":  json.RawMessage(bj),
		"created_at": d.CreatedAt,
		"updated_at": time.Now().UTC(),
	})
}

// GET /vault/{id}/search?q=...
func handle_search(w http.ResponseWriter, r *http.Request, vaultID int64) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "db_open_failed", err.Error())
		return
	}

	rows, err := vdb.Query(`
		SELECT d.id, d.vault_id, d.schema_id, d.title, d.data_json, d.created_at, d.updated_at
		FROM document_fts f
		JOIN documents d ON d.id=f.rowid
		WHERE document_fts MATCH ?
		ORDER BY rank LIMIT 100`, q)
	if err != nil {
		json_err(w, http.StatusInternalServerError, "query_failed", err.Error())
		return
	}
	defer rows.Close()

	var docs []document
	for rows.Next() {
		var d document
		if err := rows.Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
			json_err(w, http.StatusInternalServerError, "scan_failed", err.Error())
			return
		}
		docs = append(docs, d)
	}
	with_json(w, map[string]any{
		"q":     q,
		"count": len(docs),
		"docs":  docs,
	})
}
