package main

import "time"

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
