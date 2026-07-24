package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AutomationScript holds the schema definition for the automation_scripts table.
type AutomationScript struct {
	ent.Schema
}

func (AutomationScript) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "automation_scripts"},
	}
}

func (AutomationScript) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name"),
		field.String("type"),
		field.Text("content"),
		field.Text("description").Optional(),
		field.JSON("input_params", []any{}).Default([]any{}),
		field.JSON("output_params", []any{}).Default([]any{}),
		field.Int("timeout").Default(0),
		field.String("run_as").Optional(),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.Int("version").Default(1),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (AutomationScript) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
