package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NotificationChannel holds the schema definition for the notification_channels table.
type NotificationChannel struct {
	ent.Schema
}

func (NotificationChannel) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "notification_channels"},
	}
}

func (NotificationChannel) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.String("type").Optional(),
		field.JSON("config", map[string]any{}).Default(map[string]any{}),
		field.Bool("enabled").Default(true),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (NotificationChannel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
