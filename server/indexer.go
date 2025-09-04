package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

type indexer struct {
	db *sql.DB
}

func new_indexer() *indexer {
	var path = fmt.Sprintf("%s/%s/indexer.db", EXE_DIR, DATA_DIR)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		log.Fatal(err)
	}
	return &indexer{db: db}
}

func (ix *indexer) init_index_db() error {
	stmts := []string{
		`PRAGMA foreign_keys=ON;`,
		`CREATE TABLE IF NOT EXISTS vaults (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL
		);`,
	}
	for _, s := range stmts {
		if _, err := ix.db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func (ix *indexer) list_vaults() ([]vault, error) {
	rows, err := ix.db.Query(`SELECT id, name, slug, created_at FROM vaults ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []vault
	for rows.Next() {
		var v vault
		if err := rows.Scan(&v.ID, &v.Name, &v.Slug, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (ix *indexer) get_vault(id int64) (vault, error) {
	var v vault
	err := ix.db.QueryRow(`SELECT id, name, slug, created_at FROM vaults WHERE id=?`, id).
		Scan(&v.ID, &v.Name, &v.Slug, &v.CreatedAt)
	return v, err
}

func (ix *indexer) create_vault(name, slug string) (vault, error) {
	name = strings_trim_space(name)
	slug = strings_trim_space(slug)
	if name == "" || slug == "" {
		return vault{}, errors.New("name and slug required")
	}
	now := time.Now().UTC()
	res, err := ix.db.Exec(`INSERT INTO vaults(name, slug, created_at) VALUES(?,?,?)`, name, slug, now)
	if err != nil {
		return vault{}, err
	}
	id, _ := res.LastInsertId()
	// create/init per-vault db file
	if err := ensure_vault_db_created(slug); err != nil {
		return vault{}, err
	}
	return vault{ID: id, Name: name, Slug: slug, CreatedAt: now}, nil
}

func ensure_vault_db_created(slug string) error {
	path := vault_db_path(slug)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	vdb, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer vdb.Close()
	return init_vault_db(vdb)
}
