package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CmdbAsset holds the schema definition for the cmdb_ci table.
type CmdbAsset struct {
	ent.Schema
}

func (CmdbAsset) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "cmdb_ci"},
	}
}

func (CmdbAsset) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("type_id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.Text("description").Optional(),
		field.String("status").Optional(),
		field.JSON("attributes", map[string]any{}).Default(map[string]any{}),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.String("channel").Optional(),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (CmdbAsset) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("type_id"),
	}
}
