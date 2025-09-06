package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func create_db_folders() {
	var err error
	err = os.MkdirAll(filepath.Join(EXE_DIR, DATA_DIR), os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}
	err = os.MkdirAll(filepath.Join(EXE_DIR, DATA_DIR, VAULT_DIR), os.ModePerm)
	if err != nil {
		log.Fatal(err)
	}
}

func get_exe_dir() {
	ex, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}
	EXE_DIR = filepath.Dir(ex)
}

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

func strings_trim_space(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	for i < j && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
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

type api_error struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func with_json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func json_created(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(v)
}

func json_err(w http.ResponseWriter, code int, errCode, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(api_error{Error: errCode, Message: msg})
}

func must_method(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	json_err(w, http.StatusMethodNotAllowed, "method_not_allowed", "use one of: "+strings.Join(methods, ", "))
	return false
}

func path_parts(r *http.Request) []string {
	p := strings.Trim(r.URL.Path, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func parse_int64(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, errors.New("invalid_id")
	}
	return id, nil
}
