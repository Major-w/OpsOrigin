package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NotificationHistory holds the schema definition for the notification_history table.
type NotificationHistory struct {
	ent.Schema
}

func (NotificationHistory) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "notification_history"},
	}
}

func (NotificationHistory) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("channel_id").Optional(),
		field.String("event").Optional(),
		field.Text("title").Optional(),
		field.Text("content").Optional(),
		field.String("status").Optional(),
		field.Text("error").Optional(),
		field.Time("created_at"),
	}
}

func (NotificationHistory) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "created_at"),
	}
}
