package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AgentHost holds the schema definition for the agent_hosts table.
type AgentHost struct {
	ent.Schema
}

func (AgentHost) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "agent_hosts"},
	}
}

func (AgentHost) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("fingerprint").Optional(),
		field.String("name").Optional(),
		field.String("type").Optional(),
		field.String("address").Optional(),
		field.Int("port").Optional(),
		field.String("os_type").Optional(),
		field.String("os_version").Optional(),
		field.String("auth_type").Optional(),
		field.String("ssh_user").Optional(),
		field.String("status").Default("offline"),
		field.String("proxy_id").Optional(),
		field.String("install_method").Optional(),
		field.String("version").Optional(),
		field.String("cpu").Optional(),
		field.String("memory").Optional(),
		field.String("disk").Optional(),
		field.String("hostname").Optional(),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.String("environment").Optional(),
		field.JSON("capabilities", []string{}).Default([]string{}),
		field.JSON("ips", []string{}).Default([]string{}),
		field.JSON("channels", []string{}).Default([]string{}),
		field.Time("last_seen").Optional(),
		field.Time("last_heartbeat").Optional(),
		field.Time("first_seen").Optional(),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (AgentHost) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
		index.Fields("fingerprint"),
	}
}
