package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AutomationWorkflow holds the schema definition for the automation_workflows table.
type AutomationWorkflow struct {
	ent.Schema
}

func (AutomationWorkflow) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "automation_workflows"},
	}
}

func (AutomationWorkflow) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name"),
		field.Text("description").Optional(),
		field.JSON("steps", []any{}).Default([]any{}),
		field.JSON("input_params", []any{}).Default([]any{}),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (AutomationWorkflow) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
