package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OtohaCatalogImageModel is one image model in a group's Otoha catalog (TASK-73): what the Otoha app may draw with,
// the sizes and qualities it offers, whether it edits images, and (optionally) the price of an image per size tier.
// The app reads these in a separate list from the chat models, so older apps do not see them.
type OtohaCatalogImageModel struct {
	ent.Schema
}

func (OtohaCatalogImageModel) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "otoha_catalog_image_models"},
	}
}

func (OtohaCatalogImageModel) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OtohaCatalogImageModel) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.String("model_id").
			MaxLen(200).
			NotEmpty().
			Comment("Model ID the app sends to the images endpoints."),
		field.String("name").
			MaxLen(200).
			Default(""),
		field.String("description").
			Default("").
			SchemaType(map[string]string{dialect.Postgres: "text"}),
		field.Bool("enabled").
			Default(true),
		field.Int("sort_order").
			Default(0),
		field.JSON("sizes", []string{}).
			Optional().
			Comment("Sizes the app may ask for, WIDTHxHEIGHT."),
		field.JSON("qualities", []string{}).
			Optional().
			Comment("Quality settings the app may ask for."),
		field.Bool("edits").
			Default(false).
			Comment("Whether the model edits images or takes reference images."),
		field.Float("price_1k").
			Optional().
			Nillable().
			Comment("Price of one image in the 1K size tier before the group's rate; NULL follows billing."),
		field.Float("price_2k").
			Optional().
			Nillable(),
		field.Float("price_4k").
			Optional().
			Nillable(),
		field.String("source").
			MaxLen(16).
			Default("manual").
			Comment("manual: the admin added it; auto: made from the group's accounts (TASK-76)."),
		field.JSON("manual_fields", []string{}).
			Optional().
			Comment("Fields the admin changed on an auto entry; a refresh from the accounts leaves them."),
		field.Time("unavailable_since").
			Optional().
			Nillable().
			Comment("When the group's accounts stopped listing the model; NULL while they list it."),
	}
}

func (OtohaCatalogImageModel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group", Group.Type).
			Unique().
			Required().
			Field("group_id"),
	}
}

func (OtohaCatalogImageModel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "model_id").Unique(),
		index.Fields("group_id", "sort_order"),
	}
}
