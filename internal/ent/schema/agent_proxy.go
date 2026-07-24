package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AgentProxy holds the schema definition for the agent_proxies table.
type AgentProxy struct {
	ent.Schema
}

func (AgentProxy) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "agent_proxies"},
	}
}

func (AgentProxy) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.String("address").Optional(),
		field.Int("port").Optional(),
		field.String("status").Default("offline"),
		field.Int("agent_count").Optional(),
		field.String("version").Optional(),
		field.Time("last_seen").Optional(),
		field.Time("created_at"),
	}
}

func (AgentProxy) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
