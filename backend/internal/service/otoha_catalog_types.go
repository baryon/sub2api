package service

import "context"

// OtohaCatalog is the model catalog an Otoha app reads with its key (`GET /v1/models?client=otoha`), and the copy
// carried in the configuration a claim code is exchanged for (TASK-54, TASK-55). The admin edits it per group;
// it lists only the models the group allows. It never carries upstream accounts, cost prices or internal
// instructions.
type OtohaCatalog struct {
	// Schema is this format's version.
	Schema int `json:"schema"`
	// Revision changes whenever the catalog or a price in it changes.
	Revision string              `json:"revision"`
	Models   []OtohaCatalogModel `json:"models"`
	// ImageModels are the models the app draws with (TASK-73), apart from Models so an older app, which reads only
	// Models, never lists one as a chat model. Empty when the group has none.
	ImageModels []OtohaImageModel `json:"imageModels"`
	// VideoModels, VoiceModels and QuickModels are the video, voice and quick-judgement (Jev) models an API key reaches
	// (TASK-81), each apart from Models so an app that reads only Models never lists one as a chat model. The Otoha app
	// cannot use them yet (their App is false). Empty when the group has none.
	VideoModels []OtohaVideoModel `json:"videoModels"`
	VoiceModels []OtohaVoiceModel `json:"voiceModels"`
	QuickModels []OtohaQuickModel `json:"quickModels"`
}

// OtohaCatalogModel is one model as the app shows and chooses it.
type OtohaCatalogModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Inputs the model takes: "text", "image".
	Inputs []string `json:"inputs"`
	// Images repeats whether Inputs has "image", for apps that read the purchase design's first catalog shape.
	Images bool `json:"images"`
	// Tools is whether the model calls tools.
	Tools bool `json:"tools"`
	// Context is the usable context in tokens; MaxOutput the longest reply in tokens; 0 when not known.
	Context   int `json:"context,omitempty"`
	MaxOutput int `json:"maxOutput,omitempty"`
	// Reasoning lists the reasoning levels the model takes, lowest first.
	Reasoning        []string `json:"reasoning,omitempty"`
	DefaultReasoning string   `json:"defaultReasoning,omitempty"`
	// Cost is the price tier: "low", "standard" or "high".
	Cost string `json:"cost"`
	// Price is what a request costs the user, after the group's rate.
	Price OtohaModelPrice `json:"price"`
	// Speed is "fast", "standard" or "slow".
	Speed string `json:"speed,omitempty"`
	// Strengths rates each domain ("planning", "writing", "coding", "research", "data", "summarize", "vision",
	// "translation"): "strong", "usable" or "avoid". A domain not listed is not known.
	Strengths map[string]string `json:"strengths,omitempty"`
	// Complexity is the hardest work the model is suited to: "simple", "medium" or "complex".
	Complexity string `json:"complexity,omitempty"`
	// Roles: "lead" (understands the user, plans, chooses tools and models), "execute" (does one task or step).
	Roles []string `json:"roles,omitempty"`
	// Use is the purchase design's first catalog field (default, writing, planning, fast, summarize, deep, coding,
	// web), kept for apps that read only it.
	Use []string `json:"use,omitempty"`
	// ProfileSource says what the profile rests on: "vendor", "evaluation" or "admin".
	ProfileSource string `json:"profileSource,omitempty"`
	// API is the format the app calls the model in, with the same key and gateway (TASK-64):
	// "anthropic-messages" (POST /v1/messages), "deepseek-responses" (DeepSeek's own Responses dialect, passed
	// through, POST /v1/responses) or "openai-responses" (POST /v1/responses). Every model the server lists has one.
	API string `json:"api,omitempty"`
	// PriceTiers is the price by prompt length (TASK-67): the first tier is the base price (aboveInputTokens 0,
	// equal to Price) and each later tier applies to a whole request whose input is above (or, with inclusive, at
	// least) aboveInputTokens. Prices are in Price's currency and unit, after the group's rate.
	PriceTiers []OtohaPriceTier `json:"priceTiers"`
	// Compaction tells whether the model service can compact a conversation itself.
	Compaction OtohaCompaction `json:"compaction"`
	// Thinking is which thinking settings the model accepts (TASK-70); null when not known.
	Thinking *OtohaThinking `json:"thinking"`
	// Levels is the profile per thinking level (TASK-70): the hardest complexity each setting and effort is suited
	// to and how much more output it costs. Empty when not known; Complexity above stays for older apps.
	Levels []OtohaThinkingLevel `json:"levels"`
}

// OtohaThinking is what a model accepts for thinking, from the vendor's documentation or the admin.
type OtohaThinking struct {
	// CanBeOff is whether thinking can be turned off; null when not known.
	CanBeOff *bool `json:"canBeOff"`
	// Settings are the settings the model accepts: "off", "between_tools", "adaptive" or "budget".
	Settings []OtohaThinkingSetting `json:"settings"`
}

// OtohaThinkingSetting is one accepted setting; MaxEffort is the highest effort at which it is accepted (empty:
// at every effort).
type OtohaThinkingSetting struct {
	Value     string `json:"value"`
	MaxEffort string `json:"maxEffort,omitempty"`
}

// OtohaThinkingLevel is the profile of one thinking level: Setting and (optionally) Effort, the hardest
// Complexity ("simple", "medium", "complex") it is suited to, and ExtraOutput ("low", "medium", "high"), how much
// more output the thinking costs.
type OtohaThinkingLevel struct {
	Setting     string `json:"setting"`
	Effort      string `json:"effort,omitempty"`
	Complexity  string `json:"complexity"`
	ExtraOutput string `json:"extraOutput"`
}

// OtohaPriceTier is the price of requests whose input is longer than a bound.
type OtohaPriceTier struct {
	// AboveInputTokens is the bound in input tokens (cached tokens included); 0 for the base price.
	AboveInputTokens int `json:"aboveInputTokens"`
	// Inclusive makes the tier apply at AboveInputTokens itself, not only above it.
	Inclusive   bool     `json:"inclusive"`
	Input       float64  `json:"input"`
	Output      float64  `json:"output"`
	CachedInput *float64 `json:"cachedInput,omitempty"`
}

// OtohaCompaction says whether the model service compacts a conversation itself: Kind is "none", "anthropic"
// (context-management compaction edit) or "openai" (the responses compaction); MinTrigger is the smallest input
// trigger the service takes, in tokens, when it has one.
type OtohaCompaction struct {
	Kind       string `json:"kind"`
	MinTrigger int    `json:"minTrigger,omitempty"`
}

// OtohaModelPrice is a model's sale price in US dollars per million tokens.
type OtohaModelPrice struct {
	Currency    string   `json:"currency"`
	Per         string   `json:"per"`
	Input       float64  `json:"input"`
	Output      float64  `json:"output"`
	CachedInput *float64 `json:"cachedInput,omitempty"`
}

// OtohaCatalogReader gives a group's Otoha catalog: nil (and no error) when the group has none.
type OtohaCatalogReader interface {
	CatalogForGroup(ctx context.Context, groupID int64) (*OtohaCatalog, error)
}

// SubscriptionPlanNamer gives a subscription plan's name by its id (TASK-60: what `/v1/usage` calls the plan).
type SubscriptionPlanNamer interface {
	PlanName(ctx context.Context, id int64) (string, error)
}
