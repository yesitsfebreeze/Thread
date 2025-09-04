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
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(tplFS))))
	http.HandleFunc("/", handle_index)
	http.HandleFunc("/vaults", handle_create_vault)
	http.HandleFunc("/vault/", handle_vault_router)
}

func handle_index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	items, err := ix.list_vaults()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := tpl.ExecuteTemplate(w, "index.html", map[string]any{"vaults": items}); err != nil {
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
	v, err := ix.create_vault(name, slug)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		_ = tpl.ExecuteTemplate(w, "_vault_row.html", v)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", v.ID), http.StatusSeeOther)
}

// /vault/{id} (+ subroutes)
func handle_vault_router(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	vID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// nested: /vault/{id}
	if len(parts) == 2 && r.Method == http.MethodGet {
		handle_vault_show(w, r, vID)
		return
	}

	// nested: /vault/{id}/new | /schemas | /documents | /search
	if len(parts) == 3 {
		switch parts[2] {
		case "new":
			if r.Method == http.MethodGet {
				handle_new_document_form(w, r, vID)
				return
			}
		case "schemas":
			if r.Method == http.MethodPost {
				handle_create_schema(w, r, vID)
				return
			}
		case "documents":
			if r.Method == http.MethodPost {
				handle_create_document(w, r, vID)
				return
			}
		case "search":
			if r.Method == http.MethodGet {
				handle_search(w, r, vID)
				return
			}
		}
	}

	// nested: /vault/{id}/doc/{docID}/edit
	if len(parts) == 5 && parts[2] == "doc" && parts[4] == "edit" {
		docID, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			handle_edit_document_form(w, r, vID, docID)
			return
		case http.MethodPost:
			handle_update_document(w, r, vID, docID)
			return
		default:
			http.Error(w, "method not allowed", 405)
			return
		}
	}

	http.NotFound(w, r)
}

func handle_vault_show(w http.ResponseWriter, r *http.Request, vaultID int64) {
	v, err := ix.get_vault(vaultID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var s *schema
	var tmp schema
	if err := vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at
	                         FROM schemas WHERE is_active=1
	                         ORDER BY version DESC LIMIT 1`).
		Scan(&tmp.ID, &tmp.VaultID, &tmp.Version, &tmp.Title, &tmp.JSON, &tmp.IsActive, &tmp.CreatedAt); err == nil {
		s = &tmp
	}

	rows, err := vdb.Query(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at
	                        FROM documents ORDER BY updated_at DESC LIMIT 50`)
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
	if err := tpl.ExecuteTemplate(w, "vault.html", map[string]any{"vault": v, "schema": s, "documents": docs}); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

// --- schemas (per-vault db) --------------------------------------------------

func handle_create_schema(w http.ResponseWriter, r *http.Request, vaultID int64) {
	title := strings.TrimSpace(r.FormValue("title"))
	jsonStr := strings.TrimSpace(r.FormValue("json_schema"))
	if title == "" || jsonStr == "" {
		http.Error(w, "title and json_schema required", 400)
		return
	}

	var sd schemaDoc
	if err := json.Unmarshal([]byte(jsonStr), &sd); err != nil {
		http.Error(w, "invalid json_schema: "+err.Error(), 400)
		return
	}
	if err := validate_schema(sd); err != nil {
		http.Error(w, "invalid schema: "+err.Error(), 400)
		return
	}

	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var version int
	_ = vdb.QueryRow(`SELECT COALESCE(MAX(version),0)+1 FROM schemas`).Scan(&version)

	tx, _ := vdb.Begin()
	_, _ = tx.Exec(`UPDATE schemas SET is_active=0`)
	_, err = tx.Exec(`INSERT INTO schemas(vault_id, version, title, json, is_active, created_at)
	                  VALUES(?,?,?,?,1,?)`, vaultID, version, title, jsonStr, time.Now().UTC())
	if err != nil {
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
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", vaultID), http.StatusSeeOther)
}

// --- documents (per-vault db) -----------------------------------------------

func handle_new_document_form(w http.ResponseWriter, r *http.Request, vaultID int64) {
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var s schema
	if err := vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at
	                        FROM schemas WHERE is_active=1 ORDER BY version DESC LIMIT 1`).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt); err != nil {
		http.Error(w, "no active schema", 400)
		return
	}
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)
	_ = tpl.ExecuteTemplate(w, "doc_new.html", map[string]any{"vault_id": vaultID, "schema": s, "sd": sd})
}

func handle_create_document(w http.ResponseWriter, r *http.Request, vaultID int64) {
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var s schema
	if err := vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at
	                        FROM schemas WHERE is_active=1 ORDER BY version DESC LIMIT 1`).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt); err != nil {
		http.Error(w, "no active schema", 400)
		return
	}
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)

	m := map[string]any{}
	title := strings.TrimSpace(r.FormValue("__title"))
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
		default:
			m[f.Name] = val
		}
	}
	bj, _ := json.Marshal(m)
	if title == "" {
		title = derive_title(m)
	}

	tx, _ := vdb.Begin()
	res, err := tx.Exec(`INSERT INTO documents(vault_id, schema_id, title, data_json, created_at, updated_at)
	                     VALUES(?,?,?,?,?,?)`, vaultID, s.ID, title, string(bj), time.Now().UTC(), time.Now().UTC())
	if err != nil {
		_ = tx.Rollback()
		http.Error(w, err.Error(), 500)
		return
	}
	docID, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO document_fts(rowid, title, data_text) VALUES(?,?,?)`,
		docID, title, flatten_json_for_fts(m)); err != nil {
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
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", vaultID), http.StatusSeeOther)
}

// --- edit/update use per-vault path: /vault/{vaultID}/doc/{docID}/edit -------

func handle_edit_document_form(w http.ResponseWriter, r *http.Request, vaultID, docID int64) {
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var d document
	if err := vdb.QueryRow(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at
	                        FROM documents WHERE id=?`, docID).
		Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
		http.NotFound(w, r)
		return
	}
	var s schema
	_ = vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE id=?`, d.SchemaID).
		Scan(&s.ID, &s.VaultID, &s.Version, &s.Title, &s.JSON, &s.IsActive, &s.CreatedAt)
	var sd schemaDoc
	_ = json.Unmarshal([]byte(s.JSON), &sd)
	var data map[string]any
	_ = json.Unmarshal([]byte(d.DataJSON), &data)
	_ = tpl.ExecuteTemplate(w, "doc_edit.html", map[string]any{"doc": d, "schema": s, "sd": sd, "data": data})
}

func handle_update_document(w http.ResponseWriter, r *http.Request, vaultID, docID int64) {
	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var d document
	if err := vdb.QueryRow(`SELECT id, vault_id, schema_id, title, data_json, created_at, updated_at
	                        FROM documents WHERE id=?`, docID).
		Scan(&d.ID, &d.VaultID, &d.SchemaID, &d.Title, &d.DataJSON, &d.CreatedAt, &d.UpdatedAt); err != nil {
		http.NotFound(w, r)
		return
	}
	var s schema
	_ = vdb.QueryRow(`SELECT id, vault_id, version, title, json, is_active, created_at FROM schemas WHERE id=?`, d.SchemaID).
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

	if _, err := vdb.Exec(`UPDATE documents SET title=?, data_json=?, updated_at=? WHERE id=?`,
		title, string(bj), time.Now().UTC(), docID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_, _ = vdb.Exec(`INSERT INTO document_fts(document_fts, rowid, title, data_text) VALUES('delete', ?, '', '')`, docID)
	_, _ = vdb.Exec(`INSERT INTO document_fts(rowid, title, data_text) VALUES(?,?,?)`, docID, title, flatten_json_for_fts(m))

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/vault/%d", vaultID))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/vault/%d", vaultID), http.StatusSeeOther)
}

// --- search (per-vault db) ---------------------------------------------------

func handle_search(w http.ResponseWriter, r *http.Request, vaultID int64) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	vdb, err := vs.db_for_vault(vaultID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	rows, err := vdb.Query(`
		SELECT d.id, d.vault_id, d.schema_id, d.title, d.data_json, d.created_at, d.updated_at
		FROM document_fts f
		JOIN documents d ON d.id=f.rowid
		WHERE document_fts MATCH ?
		ORDER BY rank LIMIT 100`, q)
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
	v, _ := ix.get_vault(vaultID)
	// HTMX partial?
	if r.Header.Get("HX-Request") == "true" {
		_ = tpl.ExecuteTemplate(w, "_doc_list.html", docs)
		return
	}
	_ = tpl.ExecuteTemplate(w, "vault.html", map[string]any{"vault": v, "schema": nil, "documents": docs})
}
