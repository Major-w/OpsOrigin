package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IamGroup holds the schema definition for the iam_groups table.
type IamGroup struct {
	ent.Schema
}

func (IamGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "iam_groups"},
	}
}

func (IamGroup) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.Text("description").Optional(),
		field.JSON("user_ids", []string{}).Default([]string{}),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (IamGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
