package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AutomationScheduler holds the schema definition for the automation_scheduled_tasks table.
type AutomationScheduler struct {
	ent.Schema
}

func (AutomationScheduler) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "automation_scheduled_tasks"},
	}
}

func (AutomationScheduler) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name"),
		field.String("cron_expr"),
		field.String("task_type"),
		field.String("target_id"),
		field.JSON("agent_ids", []string{}).Default([]string{}),
		field.JSON("inputs", map[string]any{}).Default(map[string]any{}),
		field.Bool("enabled").Default(false),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.Time("last_run_at").Optional(),
		field.Time("next_run_at").Optional(),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (AutomationScheduler) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("enabled"),
	}
}
