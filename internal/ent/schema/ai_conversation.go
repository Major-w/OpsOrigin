package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AiConversation holds the schema definition for the ai_conversations table.
type AiConversation struct {
	ent.Schema
}

func (AiConversation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "ai_conversations"},
	}
}

func (AiConversation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("agent_id").Optional(),
		field.Text("title").Optional(),
		field.JSON("messages", []any{}).Default([]any{}),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (AiConversation) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "updated_at"),
	}
}
