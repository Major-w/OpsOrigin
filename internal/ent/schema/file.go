package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// File holds the schema definition for the files table.
type File struct {
	ent.Schema
}

func (File) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "files"},
	}
}

func (File) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("file_id"),
		field.String("tenant_id").Default("default"),
		field.String("original_name"),
		field.String("stored_path"),
		field.Int64("size_bytes").Default(0),
		field.String("checksum").Optional(),
		field.String("checksum_algo").Default("sha256"),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (File) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
