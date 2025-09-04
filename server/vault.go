package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
)

type vault_store struct {
	mu    sync.Mutex
	cache map[int64]*sql.DB // keyed by vault id
}

func new_vault_store() *vault_store {
	return &vault_store{cache: make(map[int64]*sql.DB)}
}

func vaults_dir() string { return "vaults" }
func vault_db_path(slug string) string {
	return filepath.Join(vaults_dir(), slug+".db")
}

// db_for_vault returns an opened *sql.DB for the vault id, initializing the file if needed.
func (vs *vault_store) db_for_vault(vaultID int64) (*sql.DB, error) {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	if db, ok := vs.cache[vaultID]; ok {
		return db, nil
	}

	// fetch slug from the index
	v, err := ix.get_vault(vaultID)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(vaults_dir(), 0o755); err != nil {
		return nil, err
	}

	path := vault_db_path(v.Slug)
	vdb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := init_vault_db(vdb); err != nil {
		_ = vdb.Close()
		return nil, err
	}

	vs.cache[vaultID] = vdb
	return vdb, nil
}

// init_vault_db creates the per-vault tables (schemas, documents, FTS5).
func init_vault_db(db *sql.DB) error {
	stmts := []string{
		`PRAGMA foreign_keys=ON;`,
		`CREATE TABLE IF NOT EXISTS schemas (
			id INTEGER PRIMARY KEY,
			vault_id INTEGER NOT NULL, -- local echo for convenience
			version INTEGER NOT NULL,
			title TEXT NOT NULL,
			json TEXT NOT NULL,
			is_active INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_schemas_version ON schemas(version);`,
		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY,
			vault_id INTEGER NOT NULL, -- local echo for convenience
			schema_id INTEGER NOT NULL REFERENCES schemas(id),
			title TEXT NOT NULL,
			data_json TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS document_fts
			USING fts5(title, data_text, content='documents', content_rowid='id');`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}
