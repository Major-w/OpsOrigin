package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IamRole holds the schema definition for the iam_roles table.
type IamRole struct {
	ent.Schema
}

func (IamRole) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "iam_roles"},
	}
}

func (IamRole) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.String("display_name").Optional(),
		field.Text("description").Optional(),
		field.JSON("permissions", []string{}).Default([]string{}),
		field.Bool("is_built_in").Default(false),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (IamRole) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
