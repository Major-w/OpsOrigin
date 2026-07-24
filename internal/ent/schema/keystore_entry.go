package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// KeystoreEntry holds the schema definition for the keystore_entries table.
type KeystoreEntry struct {
	ent.Schema
}

func (KeystoreEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "keystore_entries"},
	}
}

func (KeystoreEntry) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.String("type").Optional(),
		field.Text("content"),
		field.String("fingerprint").Optional(),
		field.Int("size").Default(0),
		field.String("algorithm").Optional(),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (KeystoreEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "type"),
	}
}
