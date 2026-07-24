package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ItsmOncall holds the schema definition for the oncall_shifts table.
type ItsmOncall struct {
	ent.Schema
}

func (ItsmOncall) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oncall_shifts"},
	}
}

func (ItsmOncall) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("user_id").Optional(),
		field.Time("start_time").Optional(),
		field.Time("end_time").Optional(),
		field.Time("created_at"),
	}
}

func (ItsmOncall) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "start_time"),
	}
}
