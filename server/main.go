package main

import (
	"log"
	"net/http"

	_ "modernc.org/sqlite"
)

var (
	EXE_DIR string
	ix      *indexer
	vs      *vault_store
)

func main() {
	get_exe_dir()
	create_db_folders()
	ix = new_indexer()
	if err := ix.init_index_db(); err != nil {
		log.Fatal(err)
	}
	defer ix.db.Close()

	vs = new_vault_store()

	register_routes()

	log.Println("listening on http://localhost:" + PORT)
	log.Fatal(http.ListenAndServe("localhost:"+PORT, nil))
}
