package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AgentTask holds the schema definition for the agent_tasks table.
type AgentTask struct {
	ent.Schema
}

func (AgentTask) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "agent_tasks"},
	}
}

func (AgentTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("agent_id").Optional(),
		field.String("action").Optional(),
		field.String("script").Optional(),
		field.JSON("params", map[string]any{}).Default(map[string]any{}),
		field.Int("timeout").Optional(),
		field.String("run_as").Optional(),
		field.String("status").Default("pending"),
		field.Text("output").Optional(),
		field.Int("exit_code").Optional(),
		field.Int64("duration_ms").Default(0),
		field.Text("error").Optional(),
		// §5 文件分发字段
		field.String("file_id").Optional(),
		field.Text("file_url").Optional(),
		field.Text("file_path").Optional(),
		field.String("expected_checksum").Optional(),
		field.String("checksum_algo").Optional(),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (AgentTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("agent_id", "created_at"),
		index.Fields("tenant_id"),
	}
}
