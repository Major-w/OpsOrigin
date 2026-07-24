package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ItsmKnowledge holds the schema definition for the knowledge_articles table.
type ItsmKnowledge struct {
	ent.Schema
}

func (ItsmKnowledge) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "knowledge_articles"},
	}
}

func (ItsmKnowledge) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.Text("title").Optional(),
		field.Text("content").Optional(),
		field.String("category").Optional(),
		field.JSON("tags", []string{}).Default([]string{}),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (ItsmKnowledge) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "category"),
	}
}
