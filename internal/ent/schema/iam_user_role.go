package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// IamUserRole holds the schema definition for the iam_user_roles table.
type IamUserRole struct {
	ent.Schema
}

func (IamUserRole) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "iam_user_roles"},
	}
}

func (IamUserRole) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_id"),
		field.String("tenant_id"),
		field.String("role_id"),
	}
}
