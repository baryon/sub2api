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

// OtohaCatalogModel is one model in a group's Otoha model catalog (TASK-54): what the Otoha app shows and chooses
// from, with its capabilities, profile and sale price.
type OtohaCatalogModel struct {
	ent.Schema
}

func (OtohaCatalogModel) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "otoha_catalog_models"},
	}
}

func (OtohaCatalogModel) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OtohaCatalogModel) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.String("model_id").
			MaxLen(200).
			NotEmpty().
			Comment("Model ID the app sends."),
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
		field.JSON("inputs", []string{}).
			Optional(),
		field.Bool("tools").
			Default(false),
		field.Int("context_tokens").
			Default(0),
		field.Int("max_output_tokens").
			Default(0),
		field.JSON("reasoning_levels", []string{}).
			Optional(),
		field.String("default_reasoning").
			MaxLen(32).
			Default(""),
		field.String("speed").
			MaxLen(20).
			Default(""),
		field.JSON("strengths", map[string]string{}).
			Optional(),
		field.String("complexity").
			MaxLen(20).
			Default(""),
		field.JSON("roles", []string{}).
			Optional(),
		field.JSON("uses", []string{}).
			Optional(),
		field.String("profile_source").
			MaxLen(20).
			Default(""),
		field.String("cost_tier").
			MaxLen(20).
			Default("").
			Comment("Empty derives the tier from the sale price."),
	}
}

func (OtohaCatalogModel) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group", Group.Type).
			Unique().
			Required().
			Field("group_id"),
	}
}

func (OtohaCatalogModel) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "model_id").Unique(),
		index.Fields("group_id", "sort_order"),
	}
}
