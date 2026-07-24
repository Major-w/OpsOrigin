package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// NotificationPolicy holds the schema definition for the notification_policies table.
type NotificationPolicy struct {
	ent.Schema
}

func (NotificationPolicy) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "notification_policies"},
	}
}

func (NotificationPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.String("id"),
		field.String("tenant_id"),
		field.String("name").Optional(),
		field.String("event").Optional(),
		field.JSON("channel_ids", []string{}).Default([]string{}),
		field.String("template").Optional(),
		field.Text("quiet_hours").Optional(),
		field.Bool("enabled").Default(true),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

func (NotificationPolicy) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id"),
	}
}
