package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// The Otoha model catalog (TASK-54): per group, the admin lists the models an Otoha app may choose, with their
// capabilities, profile and price. The app reads it with its key (`GET /v1/models?client=otoha`); a group without
// entries has no catalog and keeps the plain model list.

const (
	// OtohaCatalogSchemaVersion is OtohaCatalog.Schema.
	OtohaCatalogSchemaVersion = 1

	OtohaCostLow      = "low"
	OtohaCostStandard = "standard"
	OtohaCostHigh     = "high"

	// Cost tier thresholds on the sale price, input plus output per million tokens (US$):
	// below 5 is low, below 30 is standard, from 30 up is high.
	otohaCostLowBelow      = 5.0
	otohaCostStandardBelow = 30.0

	otohaPriceCurrency = "USD"
	otohaPricePer      = "1M tokens"

	// Why an entry is not in the app's catalog, for the admin page.
	OtohaCatalogProblemDisabled   = "disabled"
	OtohaCatalogProblemNotAllowed = "not_allowed"
	OtohaCatalogProblemNoAccount  = "no_account"
	OtohaCatalogProblemNoPrice    = "no_price"
	// A composite group cannot tell which provider serves the model on the endpoint the app calls it through (no
	// route, and its name does not say, or accounts of two providers claim it); the request would be refused.
	OtohaCatalogProblemNoRoute = "no_route"
	// The model goes to a provider whose own format the app does not speak (Gemini, TypeSafe), and the admin has not
	// chosen one.
	OtohaCatalogProblemNoNativeAPI = "no_native_api"
	// The admin chose a format whose endpoint does not reach the provider the model goes to.
	OtohaCatalogProblemAPIUnreachable = "api_unreachable"
	// The group's channel limits requests to the models it prices, and does not price this one.
	OtohaCatalogProblemChannelRestricted = "channel_restricted"

	// The format the app calls a model in (OtohaCatalogModel.API, TASK-64), each through the same key and gateway:
	// Anthropic Messages on POST /v1/messages, DeepSeek's own Responses dialect and OpenAI Responses on
	// POST /v1/responses.
	OtohaAPIAnthropicMessages = "anthropic-messages"
	OtohaAPIDeepSeekResponses = "deepseek-responses"
	OtohaAPIOpenAIResponses   = "openai-responses"

	otohaCatalogCacheTTL = 30 * time.Second

	otohaModelIDMaxLen     = 200
	otohaTokensMax         = 100_000_000
	otohaNameMaxLen        = 200
	otohaDescriptionMaxLen = 2000
	otohaSortOrderStep     = 10
)

var (
	ErrOtohaCatalogEntryNotFound = infraerrors.NotFound("OTOHA_CATALOG_ENTRY_NOT_FOUND", "catalog entry not found")
	ErrOtohaCatalogEntryExists   = infraerrors.Conflict("OTOHA_CATALOG_ENTRY_EXISTS", "this model is already in the group's catalog")

	otohaDomains          = []string{"planning", "writing", "coding", "research", "data", "summarize", "vision", "translation"}
	otohaStrengthLevels   = []string{"strong", "usable", "avoid"}
	otohaSpeeds           = []string{"fast", "standard", "slow"}
	otohaComplexities     = []string{"simple", "medium", "complex"}
	otohaRoles            = []string{"lead", "execute"}
	otohaUses             = []string{"default", "writing", "planning", "fast", "summarize", "deep", "coding", "web"}
	otohaProfileSources   = []string{"vendor", "evaluation", "admin"}
	otohaInputs           = []string{"text", "image", "audio", "video", "file"}
	otohaCostTiers        = []string{OtohaCostLow, OtohaCostStandard, OtohaCostHigh}
	otohaAPIs             = []string{OtohaAPIAnthropicMessages, OtohaAPIDeepSeekResponses, OtohaAPIOpenAIResponses}
	otohaReasoningPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

	// A strong domain suggests the purchase design's use; the app's first catalog shape reads only `use`.
	otohaUseForDomain = map[string]string{
		"planning":  "planning",
		"writing":   "writing",
		"coding":    "coding",
		"research":  "web",
		"summarize": "summarize",
	}
)

// OtohaCatalogEntry is one model in a group's catalog as the admin edits it.
type OtohaCatalogEntry struct {
	ID               int64             `json:"id"`
	GroupID          int64             `json:"group_id"`
	ModelID          string            `json:"model_id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Enabled          bool              `json:"enabled"`
	SortOrder        int               `json:"sort_order"`
	Inputs           []string          `json:"inputs"`
	Tools            bool              `json:"tools"`
	Context          int               `json:"context"`
	MaxOutput        int               `json:"max_output"`
	Reasoning        []string          `json:"reasoning"`
	DefaultReasoning string            `json:"default_reasoning"`
	Speed            string            `json:"speed"`
	Strengths        map[string]string `json:"strengths"`
	Complexity       string            `json:"complexity"`
	Roles            []string          `json:"roles"`
	Use              []string          `json:"use"`
	ProfileSource    string            `json:"profile_source"`
	// CostTier is the admin's price tier; empty derives it from the sale price.
	CostTier string `json:"cost_tier"`
	// API is the admin's choice of the format the app calls the model in; empty derives it from the provider.
	API       string    `json:"api"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OtohaCatalogEntryInput is what the admin submits for an entry. The order is changed with ReorderEntries.
type OtohaCatalogEntryInput struct {
	ModelID          string
	Name             string
	Description      string
	Enabled          bool
	Inputs           []string
	Tools            bool
	Context          int
	MaxOutput        int
	Reasoning        []string
	DefaultReasoning string
	Speed            string
	Strengths        map[string]string
	Complexity       string
	Roles            []string
	Use              []string
	ProfileSource    string
	CostTier         string
	API              string
}

// OtohaModelMetadata is what upstream says about a model, used to prefill an entry.
type OtohaModelMetadata struct {
	Name             string
	Description      string
	Inputs           []string
	Tools            bool
	Context          int
	MaxOutput        int
	Reasoning        []string
	DefaultReasoning string
}

// OtohaCatalogAdminEntry is an entry with what the admin needs to judge it: the upstream and sale prices, the tier
// and use the app will see, and why the app does not get it (Problem), if it does not.
type OtohaCatalogAdminEntry struct {
	OtohaCatalogEntry
	UpstreamPrice *OtohaModelPrice `json:"upstream_price"`
	SalePrice     *OtohaModelPrice `json:"sale_price"`
	EffectiveCost string           `json:"effective_cost"`
	EffectiveUse  []string         `json:"effective_use"`
	InCatalog     bool             `json:"in_catalog"`
	Problem       string           `json:"problem"`
	// RoutePlatform is, in a composite group, the provider the app's requests for the model go to; RouteModel the
	// model they are forwarded as, when an explicit route renames it.
	RoutePlatform string `json:"route_platform"`
	RouteModel    string `json:"route_model"`
	// BilledModel is the model the price is that of, when billing prices another name than the model (an account
	// maps it); PriceVaries is set when the group's accounts bill it as models of different prices, the highest shown.
	BilledModel string `json:"billed_model"`
	PriceVaries bool   `json:"price_varies"`
	// EffectiveAPI is the format the app calls the model in: the admin's, else the provider's own; empty when the
	// provider has none the app speaks.
	EffectiveAPI string `json:"effective_api"`

	lookupFailed bool
}

// OtohaCatalogAdminView is a group's catalog for the admin page, with Preview being exactly what the app receives
// (nil when the group has no entries).
type OtohaCatalogAdminView struct {
	GroupID        int64                    `json:"group_id"`
	GroupName      string                   `json:"group_name"`
	GroupPlatform  string                   `json:"group_platform"`
	RateMultiplier float64                  `json:"rate_multiplier"`
	Entries        []OtohaCatalogAdminEntry `json:"entries"`
	Preview        *OtohaCatalog            `json:"preview"`
}

// OtohaCatalogPrefill is a draft entry filled from upstream metadata, with the prices it would sell at.
type OtohaCatalogPrefill struct {
	Entry         OtohaCatalogEntry `json:"entry"`
	MetadataFound bool              `json:"metadata_found"`
	UpstreamPrice *OtohaModelPrice  `json:"upstream_price"`
	SalePrice     *OtohaModelPrice  `json:"sale_price"`
	CostTier      string            `json:"cost_tier"`
	// RoutePlatform is, in a composite group, the provider the model is routed to.
	RoutePlatform string `json:"route_platform"`
	// RouteAPI is the format the app would call the model in, derived from the provider.
	RouteAPI string `json:"route_api"`
}

// OtohaModelUsage is one model's usage through a key in a period, priced at what the user pays.
type OtohaModelUsage struct {
	Model               string  `json:"model"`
	Requests            int64   `json:"requests"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	Cost                float64 `json:"cost"`
}

// OtohaCatalogRepository stores catalog entries.
type OtohaCatalogRepository interface {
	// ListByGroup lists a group's entries by sort order, then ID.
	ListByGroup(ctx context.Context, groupID int64) ([]OtohaCatalogEntry, error)
	Create(ctx context.Context, entry *OtohaCatalogEntry) error
	Update(ctx context.Context, entry *OtohaCatalogEntry) error
	Delete(ctx context.Context, id int64) error
	// UpdateSortOrders sets the sort order of the group's entries named in orders (entry ID to order).
	UpdateSortOrders(ctx context.Context, groupID int64, orders map[int64]int) error
}

// OtohaCatalogGroupSource loads the group a catalog belongs to.
type OtohaCatalogGroupSource interface {
	GetByIDLite(ctx context.Context, id int64) (*Group, error)
}

// OtohaUpstreamPricer gives a model's token price in the group before the group's rate, per million tokens; false
// when there is none (not token-priced, or unknown).
type OtohaUpstreamPricer interface {
	UpstreamPrice(ctx context.Context, group *Group, modelID string) (OtohaModelPrice, bool)
}

// OtohaModelMetadataSource gives upstream metadata about a model as the group serves it.
type OtohaModelMetadataSource interface {
	ModelMetadata(ctx context.Context, group *Group, modelID string) (OtohaModelMetadata, error)
}

// OtohaModelRoute is where a group sends the app's requests for a model, on the endpoint of the format the app calls
// it in.
type OtohaModelRoute struct {
	// Problem is empty when the group can serve the model, else OtohaCatalogProblemNoRoute,
	// OtohaCatalogProblemNoNativeAPI, OtohaCatalogProblemAPIUnreachable, OtohaCatalogProblemChannelRestricted or
	// OtohaCatalogProblemNoAccount.
	Problem string
	// API is the format the app calls the model in (OtohaAPIAnthropicMessages, ...); empty when it cannot be told.
	API string
	// Platform is, in a composite group, the provider the requests go to; empty when it cannot be told, and for
	// a group of one provider.
	Platform string
	// UpstreamModel is the model the requests are forwarded as when a route renames it; empty means the model.
	UpstreamModel string
	// BilledModels are, when the serving accounts bill the forwarded model under the names they map it to (the
	// OpenAI gateway), those names; empty means it is billed as forwarded.
	BilledModels []string
	// Failed is set when the route could not be looked up; the result is not kept.
	Failed bool
}

// forwardedModel is the model the provider receives, and billing prices.
func (r OtohaModelRoute) forwardedModel(modelID string) string {
	if r.UpstreamModel != "" {
		return r.UpstreamModel
	}
	return modelID
}

// routedContext carries a composite route the way the gateway's request context does once the request is routed,
// so the price resolver reads the provider's channel price (as billing does) and the metadata its accounts.
func (r OtohaModelRoute) routedContext(ctx context.Context, modelID string) context.Context {
	if r.Platform == "" {
		return ctx
	}
	return WithCompositeRouteDecision(ctx, CompositeRouteDecision{
		Matched:        true,
		PublicModel:    modelID,
		TargetPlatform: r.Platform,
		UpstreamModel:  r.forwardedModel(modelID),
		Endpoint:       otohaAPIEndpoint(r.API),
	})
}

// OtohaNativeAPIForPlatform is the format the app calls a provider's models in: Anthropic Messages for Claude
// (Anthropic, and Antigravity, which serves Claude through Messages), DeepSeek's own Responses for DeepSeek, OpenAI
// Responses for OpenAI, Grok and the other providers the OpenAI gateway serves. Empty for a provider whose format the
// app does not speak (Gemini, TypeSafe).
func OtohaNativeAPIForPlatform(platform string) string {
	switch platform {
	case PlatformAnthropic, PlatformAntigravity:
		return OtohaAPIAnthropicMessages
	case PlatformDeepseek:
		return OtohaAPIDeepSeekResponses
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformMiniMax, PlatformOpenCodeGo:
		return OtohaAPIOpenAIResponses
	default:
		return ""
	}
}

// otohaAPIEndpoint is the gateway endpoint a format is called on.
func otohaAPIEndpoint(api string) string {
	if api == OtohaAPIAnthropicMessages {
		return CompositeRouteEndpointMessages
	}
	return CompositeRouteEndpointResponses
}

// OtohaModelRouting tells where the group sends a model, in which format the app calls it (api, when the admin chose
// one, else the provider's own), and whether an account is configured to serve it there.
type OtohaModelRouting interface {
	Route(ctx context.Context, group *Group, modelID, api string) OtohaModelRoute
}

// OtohaCatalogService edits group catalogs and builds the catalog the app reads.
type OtohaCatalogService struct {
	repo     OtohaCatalogRepository
	groups   OtohaCatalogGroupSource
	pricer   OtohaUpstreamPricer
	metadata OtohaModelMetadataSource
	routing  OtohaModelRouting

	cacheMu    sync.Mutex
	cache      map[int64]otohaCatalogCacheEntry
	generation map[int64]uint64
	rebuilds   singleflight.Group
	now        func() time.Time
}

// otohaCatalogItem is an entry that passes into the app's catalog, with its price before any rate.
type otohaCatalogItem struct {
	entry    OtohaCatalogEntry
	upstream OtohaModelPrice
	use      []string
	api      string
}

// otohaCatalogSnapshot is a group's evaluated catalog: hasCatalog is false when the group has no entries;
// incomplete when a lookup failed, so it is not cached.
type otohaCatalogSnapshot struct {
	hasCatalog bool
	groupRate  float64
	items      []otohaCatalogItem
	incomplete bool
}

type otohaCatalogCacheEntry struct {
	snapshot  otohaCatalogSnapshot
	expiresAt time.Time
}

var _ OtohaCatalogReader = (*OtohaCatalogService)(nil)

// OtohaCatalogRateReader gives a group's catalog priced at a given rate (a key owner's own rate in the group).
type OtohaCatalogRateReader interface {
	CatalogForGroupAtRate(ctx context.Context, groupID int64, rate float64) (*OtohaCatalog, error)
}

var _ OtohaCatalogRateReader = (*OtohaCatalogService)(nil)

// NewOtohaCatalogServiceWithDeps builds the service from its parts.
func NewOtohaCatalogServiceWithDeps(
	repo OtohaCatalogRepository,
	groups OtohaCatalogGroupSource,
	pricer OtohaUpstreamPricer,
	metadata OtohaModelMetadataSource,
	routing OtohaModelRouting,
) *OtohaCatalogService {
	return &OtohaCatalogService{
		repo:       repo,
		groups:     groups,
		pricer:     pricer,
		metadata:   metadata,
		routing:    routing,
		cache:      map[int64]otohaCatalogCacheEntry{},
		generation: map[int64]uint64{},
		now:        time.Now,
	}
}

// CatalogForGroup gives the catalog the app reads, priced at the group's rate: nil (and no error) when the group
// has no entries or does not exist. It is cached for a short while; admin edits through this service show at once.
func (s *OtohaCatalogService) CatalogForGroup(ctx context.Context, groupID int64) (*OtohaCatalog, error) {
	snapshot, err := s.snapshot(ctx, groupID)
	if err != nil || !snapshot.hasCatalog {
		return nil, err
	}
	return buildOtohaCatalog(snapshot.items, snapshot.groupRate), nil
}

// CatalogForGroupAtRate gives the group's catalog priced at a key owner's own rate in the group (a user-specific
// rate replaces the group's when billing), so the app shows what that user pays.
func (s *OtohaCatalogService) CatalogForGroupAtRate(ctx context.Context, groupID int64, rate float64) (*OtohaCatalog, error) {
	snapshot, err := s.snapshot(ctx, groupID)
	if err != nil || !snapshot.hasCatalog {
		return nil, err
	}
	return buildOtohaCatalog(snapshot.items, rate), nil
}

// snapshot reads the group's evaluated catalog from the cache, or evaluates it once for concurrent callers. A
// rebuild that started before an admin edit is not cached over it.
func (s *OtohaCatalogService) snapshot(ctx context.Context, groupID int64) (otohaCatalogSnapshot, error) {
	if s == nil || s.repo == nil {
		return otohaCatalogSnapshot{}, nil
	}
	s.cacheMu.Lock()
	if cached, ok := s.cache[groupID]; ok && s.now().Before(cached.expiresAt) {
		s.cacheMu.Unlock()
		return cached.snapshot, nil
	}
	generation := s.generation[groupID]
	s.cacheMu.Unlock()

	// Callers share the rebuild, so one caller going away does not fail the others.
	rebuildCtx := context.WithoutCancel(ctx)
	value, err, _ := s.rebuilds.Do(fmt.Sprintf("%d:%d", groupID, generation), func() (any, error) {
		snapshot, err := s.evaluateGroup(rebuildCtx, groupID)
		if err != nil {
			return otohaCatalogSnapshot{}, err
		}
		s.cacheMu.Lock()
		if s.generation[groupID] == generation && !snapshot.incomplete {
			s.cache[groupID] = otohaCatalogCacheEntry{snapshot: snapshot, expiresAt: s.now().Add(otohaCatalogCacheTTL)}
		}
		s.cacheMu.Unlock()
		return snapshot, nil
	})
	if err != nil {
		return otohaCatalogSnapshot{}, err
	}
	snapshot, _ := value.(otohaCatalogSnapshot)
	return snapshot, nil
}

func (s *OtohaCatalogService) evaluateGroup(ctx context.Context, groupID int64) (otohaCatalogSnapshot, error) {
	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		if errors.Is(err, ErrGroupNotFound) {
			return otohaCatalogSnapshot{}, nil
		}
		return otohaCatalogSnapshot{}, err
	}
	entries, err := s.repo.ListByGroup(ctx, groupID)
	if err != nil {
		return otohaCatalogSnapshot{}, err
	}
	if len(entries) == 0 {
		return otohaCatalogSnapshot{}, nil
	}
	evaluated, items := s.evaluate(ctx, group, entries)
	snapshot := otohaCatalogSnapshot{hasCatalog: true, groupRate: group.RateMultiplier, items: items}
	for _, e := range evaluated {
		if e.lookupFailed {
			snapshot.incomplete = true
		}
	}
	return snapshot, nil
}

// InvalidateGroup drops the cached catalog of a group.
func (s *OtohaCatalogService) InvalidateGroup(groupID int64) {
	if s == nil {
		return
	}
	s.cacheMu.Lock()
	delete(s.cache, groupID)
	s.generation[groupID]++
	s.cacheMu.Unlock()
}

// AdminView gives a group's entries with prices and problems, and a preview of the app's catalog.
func (s *OtohaCatalogService) AdminView(ctx context.Context, groupID int64) (*OtohaCatalogAdminView, error) {
	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	entries, err := s.repo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	view := &OtohaCatalogAdminView{
		GroupID:        group.ID,
		GroupName:      group.Name,
		GroupPlatform:  group.Platform,
		RateMultiplier: group.RateMultiplier,
		Entries:        []OtohaCatalogAdminEntry{},
	}
	if len(entries) > 0 {
		var items []otohaCatalogItem
		view.Entries, items = s.evaluate(ctx, group, entries)
		view.Preview = buildOtohaCatalog(items, group.RateMultiplier)
	}
	return view, nil
}

// CreateEntry adds a model to the end of a group's catalog.
func (s *OtohaCatalogService) CreateEntry(ctx context.Context, groupID int64, input OtohaCatalogEntryInput) (*OtohaCatalogEntry, error) {
	if _, err := s.groups.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	entry, err := normalizeOtohaCatalogEntryInput(input)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	entry.GroupID = groupID
	entry.SortOrder = otohaSortOrderStep
	for _, e := range existing {
		if e.SortOrder+otohaSortOrderStep > entry.SortOrder {
			entry.SortOrder = e.SortOrder + otohaSortOrderStep
		}
	}
	if err := s.repo.Create(ctx, &entry); err != nil {
		return nil, err
	}
	s.InvalidateGroup(groupID)
	return &entry, nil
}

// UpdateEntry replaces an entry of the group; its place in the order stays.
func (s *OtohaCatalogService) UpdateEntry(ctx context.Context, groupID, entryID int64, input OtohaCatalogEntryInput) (*OtohaCatalogEntry, error) {
	current, err := s.groupEntry(ctx, groupID, entryID)
	if err != nil {
		return nil, err
	}
	entry, err := normalizeOtohaCatalogEntryInput(input)
	if err != nil {
		return nil, err
	}
	entry.ID = current.ID
	entry.GroupID = current.GroupID
	entry.SortOrder = current.SortOrder
	entry.CreatedAt = current.CreatedAt
	if err := s.repo.Update(ctx, &entry); err != nil {
		return nil, err
	}
	s.InvalidateGroup(groupID)
	return &entry, nil
}

// DeleteEntry removes an entry of the group.
func (s *OtohaCatalogService) DeleteEntry(ctx context.Context, groupID, entryID int64) error {
	if _, err := s.groupEntry(ctx, groupID, entryID); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, entryID); err != nil {
		return err
	}
	s.InvalidateGroup(groupID)
	return nil
}

// ReorderEntries puts the group's entries in the given order; the list must name each of them once.
func (s *OtohaCatalogService) ReorderEntries(ctx context.Context, groupID int64, entryIDs []int64) error {
	entries, err := s.repo.ListByGroup(ctx, groupID)
	if err != nil {
		return err
	}
	if len(entryIDs) != len(entries) {
		return infraerrors.BadRequest("OTOHA_CATALOG_ORDER_INVALID", "the order must list every model in the catalog once")
	}
	known := make(map[int64]bool, len(entries))
	for _, e := range entries {
		known[e.ID] = true
	}
	orders := make(map[int64]int, len(entryIDs))
	for i, id := range entryIDs {
		if !known[id] {
			return infraerrors.BadRequest("OTOHA_CATALOG_ORDER_INVALID", "the order must list every model in the catalog once")
		}
		if _, repeated := orders[id]; repeated {
			return infraerrors.BadRequest("OTOHA_CATALOG_ORDER_INVALID", "the order must list every model in the catalog once")
		}
		orders[id] = (i + 1) * otohaSortOrderStep
	}
	if err := s.repo.UpdateSortOrders(ctx, groupID, orders); err != nil {
		return err
	}
	s.InvalidateGroup(groupID)
	return nil
}

// Prefill drafts an entry for a model from upstream metadata, with the prices it would sell at. It saves nothing.
func (s *OtohaCatalogService) Prefill(ctx context.Context, groupID int64, modelID string) (*OtohaCatalogPrefill, error) {
	modelID = strings.TrimSpace(modelID)
	if err := validateOtohaModelID(modelID); err != nil {
		return nil, err
	}
	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	route := s.route(ctx, group, modelID, "")
	routedCtx := route.routedContext(ctx, modelID)
	draft := &OtohaCatalogPrefill{RoutePlatform: route.Platform, RouteAPI: route.API}
	input := OtohaCatalogEntryInput{ModelID: modelID, Enabled: true}
	if s.metadata != nil {
		if md, mdErr := s.metadata.ModelMetadata(routedCtx, group, modelID); mdErr == nil {
			draft.MetadataFound = true
			md = sanitizeOtohaModelMetadata(md)
			input.Name = md.Name
			input.Description = md.Description
			input.Inputs = md.Inputs
			input.Tools = md.Tools
			input.Context = md.Context
			input.MaxOutput = md.MaxOutput
			input.Reasoning = md.Reasoning
			input.DefaultReasoning = md.DefaultReasoning
		}
	}
	entry, err := normalizeOtohaCatalogEntryInput(input)
	if err != nil {
		return nil, err
	}
	entry.GroupID = groupID
	draft.Entry = entry
	if upstream, _, _, ok := s.routedPrice(ctx, group, modelID, route); ok {
		draft.UpstreamPrice = &upstream
		sale := otohaSalePrice(upstream, group.RateMultiplier)
		draft.SalePrice = &sale
		draft.CostTier = OtohaCostTierForPrice(sale)
	}
	return draft, nil
}

// sanitizeOtohaModelMetadata drops, value by value, upstream metadata the catalog cannot take (an unknown input
// kind, a default reasoning level the model does not list), so a prefill never fails on it.
func sanitizeOtohaModelMetadata(md OtohaModelMetadata) OtohaModelMetadata {
	out := OtohaModelMetadata{Tools: md.Tools}
	out.Name = otohaTruncate(strings.TrimSpace(md.Name), otohaNameMaxLen)
	out.Description = otohaTruncate(strings.TrimSpace(md.Description), otohaDescriptionMaxLen)
	for _, input := range md.Inputs {
		input = strings.ToLower(strings.TrimSpace(input))
		if otohaContains(otohaInputs, input) && !otohaContains(out.Inputs, input) {
			out.Inputs = append(out.Inputs, input)
		}
	}
	if md.Context > 0 {
		out.Context = md.Context
	}
	if md.MaxOutput > 0 {
		out.MaxOutput = md.MaxOutput
	}
	for _, level := range md.Reasoning {
		level = strings.ToLower(strings.TrimSpace(level))
		if otohaReasoningPattern.MatchString(level) && !otohaContains(out.Reasoning, level) {
			out.Reasoning = append(out.Reasoning, level)
		}
	}
	if def := strings.ToLower(strings.TrimSpace(md.DefaultReasoning)); otohaContains(out.Reasoning, def) {
		out.DefaultReasoning = def
	}
	return out
}

func otohaTruncate(value string, maxLen int) string {
	if len(value) <= maxLen {
		return value
	}
	runes := []rune(value)
	for len(string(runes)) > maxLen {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

func (s *OtohaCatalogService) groupEntry(ctx context.Context, groupID, entryID int64) (*OtohaCatalogEntry, error) {
	entries, err := s.repo.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].ID == entryID {
			return &entries[i], nil
		}
	}
	return nil, ErrOtohaCatalogEntryNotFound
}

// route asks where the group sends the model and in which format the app calls it; without a routing check every
// model is served as itself, in the admin's format or the group's provider's.
func (s *OtohaCatalogService) route(ctx context.Context, group *Group, modelID, api string) OtohaModelRoute {
	route := OtohaModelRoute{}
	if s.routing != nil {
		route = s.routing.Route(ctx, group, modelID, api)
	}
	if route.API == "" {
		// The admin's choice stands even when the model cannot be routed, so the admin page shows it.
		route.API = api
	}
	if route.API == "" && route.Problem == "" && group != nil {
		route.API = OtohaNativeAPIForPlatform(group.Platform)
	}
	return route
}

func (s *OtohaCatalogService) upstreamPrice(ctx context.Context, group *Group, modelID string) (OtohaModelPrice, bool) {
	if s.pricer == nil {
		return OtohaModelPrice{}, false
	}
	price, ok := s.pricer.UpstreamPrice(ctx, group, modelID)
	if !ok {
		return OtohaModelPrice{}, false
	}
	return normalizeOtohaPrice(price), true
}

// routedPrice is the model's price as billing charges it once the request is routed: the forwarded model's price at
// its provider, or, where the serving accounts bill it under the names they map it to, that name's price (the
// forwarded model's when that name has none). Accounts that bill it differently give the highest, with varies set.
func (s *OtohaCatalogService) routedPrice(ctx context.Context, group *Group, modelID string, route OtohaModelRoute) (price OtohaModelPrice, billed string, varies, ok bool) {
	routedCtx := route.routedContext(ctx, modelID)
	forwarded := route.forwardedModel(modelID)
	if len(route.BilledModels) == 0 {
		price, ok = s.upstreamPrice(routedCtx, group, forwarded)
		return price, forwarded, false, ok
	}
	for _, name := range route.BilledModels {
		candidate, priced := s.upstreamPrice(routedCtx, group, name)
		if !priced {
			name = forwarded
			if candidate, priced = s.upstreamPrice(routedCtx, group, forwarded); !priced {
				continue
			}
		}
		if ok && !otohaSamePrice(candidate, price) {
			varies = true
		}
		if !ok || candidate.Input+candidate.Output > price.Input+price.Output {
			price, billed = candidate, name
		}
		ok = true
	}
	return price, billed, varies, ok
}

func otohaSamePrice(a, b OtohaModelPrice) bool {
	if a.Input != b.Input || a.Output != b.Output || (a.CachedInput == nil) != (b.CachedInput == nil) {
		return false
	}
	return a.CachedInput == nil || *a.CachedInput == *b.CachedInput
}

// evaluate prices and checks every entry for the admin, and gives the entries that pass into the app's catalog. In a
// composite group each model is priced as billing prices it once routed: the forwarded model, at its provider.
func (s *OtohaCatalogService) evaluate(ctx context.Context, group *Group, entries []OtohaCatalogEntry) ([]OtohaCatalogAdminEntry, []otohaCatalogItem) {
	out := make([]OtohaCatalogAdminEntry, 0, len(entries))
	items := make([]otohaCatalogItem, 0, len(entries))
	for _, entry := range entries {
		item := OtohaCatalogAdminEntry{OtohaCatalogEntry: entry, EffectiveUse: otohaEffectiveUse(entry)}
		route := s.route(ctx, group, entry.ModelID, entry.API)
		forwarded := route.forwardedModel(entry.ModelID)
		item.RoutePlatform = route.Platform
		item.EffectiveAPI = route.API
		item.lookupFailed = route.Failed
		if forwarded != entry.ModelID {
			item.RouteModel = forwarded
		}
		upstream, billed, varies, priced := s.routedPrice(ctx, group, entry.ModelID, route)
		if priced {
			if billed != forwarded {
				item.BilledModel = billed
			}
			item.PriceVaries = varies
			sale := otohaSalePrice(upstream, group.RateMultiplier)
			item.UpstreamPrice = &upstream
			item.SalePrice = &sale
			item.EffectiveCost = otohaEffectiveCost(entry, sale)
		}
		switch {
		case !entry.Enabled:
			item.Problem = OtohaCatalogProblemDisabled
		case group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(entry.ModelID):
			item.Problem = OtohaCatalogProblemNotAllowed
		case route.Problem != "":
			item.Problem = route.Problem
		case !priced:
			item.Problem = OtohaCatalogProblemNoPrice
		default:
			item.InCatalog = true
			items = append(items, otohaCatalogItem{entry: entry, upstream: upstream, use: item.EffectiveUse, api: route.API})
		}
		out = append(out, item)
	}
	return out, items
}

// buildOtohaCatalog prices the catalog's models at a rate; the revision covers every price.
func buildOtohaCatalog(items []otohaCatalogItem, rate float64) *OtohaCatalog {
	catalog := &OtohaCatalog{Schema: OtohaCatalogSchemaVersion, Models: make([]OtohaCatalogModel, 0, len(items))}
	for _, item := range items {
		sale := otohaSalePrice(item.upstream, rate)
		model := otohaCatalogModel(item.entry, sale, otohaEffectiveCost(item.entry, sale), item.use)
		model.API = item.api
		catalog.Models = append(catalog.Models, model)
	}
	catalog.Revision = otohaCatalogRevision(catalog)
	return catalog
}

func otohaEffectiveCost(entry OtohaCatalogEntry, sale OtohaModelPrice) string {
	if entry.CostTier != "" {
		return entry.CostTier
	}
	return OtohaCostTierForPrice(sale)
}

func otohaCatalogModel(entry OtohaCatalogEntry, price OtohaModelPrice, cost string, use []string) OtohaCatalogModel {
	inputs := append([]string(nil), entry.Inputs...)
	if len(inputs) == 0 {
		inputs = []string{"text"}
	}
	model := OtohaCatalogModel{
		ID:               entry.ModelID,
		Name:             entry.Name,
		Description:      entry.Description,
		Inputs:           inputs,
		Images:           otohaContains(inputs, "image"),
		Tools:            entry.Tools,
		Context:          entry.Context,
		MaxOutput:        entry.MaxOutput,
		DefaultReasoning: entry.DefaultReasoning,
		Cost:             cost,
		Price:            price,
		Speed:            entry.Speed,
		Complexity:       entry.Complexity,
		ProfileSource:    entry.ProfileSource,
		Use:              use,
	}
	if model.Name == "" {
		model.Name = entry.ModelID
	}
	if len(entry.Reasoning) > 0 {
		model.Reasoning = append([]string(nil), entry.Reasoning...)
	}
	if len(entry.Strengths) > 0 {
		model.Strengths = make(map[string]string, len(entry.Strengths))
		for k, v := range entry.Strengths {
			model.Strengths[k] = v
		}
	}
	if len(entry.Roles) > 0 {
		model.Roles = append([]string(nil), entry.Roles...)
	}
	return model
}

// otohaCatalogRevision hashes what the app receives, so any change to a model, a price or the order changes it.
func otohaCatalogRevision(catalog *OtohaCatalog) string {
	body, err := json.Marshal(struct {
		Schema int                 `json:"schema"`
		Models []OtohaCatalogModel `json:"models"`
	}{catalog.Schema, catalog.Models})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8])
}

// otohaSalePrice is the price the app shows and billing charges: the token price billing resolves for the model in
// the group (the group's per-model price, else the channel price, else the price table) times the rate.
func otohaSalePrice(upstream OtohaModelPrice, rate float64) OtohaModelPrice {
	if rate < 0 {
		rate = 0
	}
	price := OtohaModelPrice{Input: upstream.Input * rate, Output: upstream.Output * rate}
	if upstream.CachedInput != nil {
		cached := *upstream.CachedInput * rate
		price.CachedInput = &cached
	}
	return normalizeOtohaPrice(price)
}

func normalizeOtohaPrice(price OtohaModelPrice) OtohaModelPrice {
	out := OtohaModelPrice{
		Currency: otohaPriceCurrency,
		Per:      otohaPricePer,
		Input:    roundOtohaPrice(price.Input),
		Output:   roundOtohaPrice(price.Output),
	}
	if price.CachedInput != nil {
		cached := roundOtohaPrice(*price.CachedInput)
		out.CachedInput = &cached
	}
	return out
}

func roundOtohaPrice(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// OtohaCostTierForPrice derives the tier from a sale price: input plus output per million tokens below US$5 is low,
// below US$30 standard, otherwise high.
func OtohaCostTierForPrice(price OtohaModelPrice) string {
	total := price.Input + price.Output
	switch {
	case total < otohaCostLowBelow:
		return OtohaCostLow
	case total < otohaCostStandardBelow:
		return OtohaCostStandard
	default:
		return OtohaCostHigh
	}
}

// otohaEffectiveUse is the admin's use, else what the profile suggests: each strong domain's use, "deep" for complex
// work and "fast" for a fast model, in the purchase design's order.
func otohaEffectiveUse(entry OtohaCatalogEntry) []string {
	if len(entry.Use) > 0 {
		return append([]string(nil), entry.Use...)
	}
	suggested := map[string]bool{}
	for domain, level := range entry.Strengths {
		if use, ok := otohaUseForDomain[domain]; ok && level == "strong" {
			suggested[use] = true
		}
	}
	if entry.Complexity == "complex" {
		suggested["deep"] = true
	}
	if entry.Speed == "fast" {
		suggested["fast"] = true
	}
	if len(suggested) == 0 {
		return nil
	}
	out := make([]string, 0, len(suggested))
	for _, use := range otohaUses {
		if suggested[use] {
			out = append(out, use)
		}
	}
	return out
}

// ---- validation ----

func otohaInvalid(format string, args ...any) error {
	return infraerrors.BadRequest("OTOHA_CATALOG_ENTRY_INVALID", fmt.Sprintf(format, args...))
}

func validateOtohaModelID(modelID string) error {
	if modelID == "" {
		return otohaInvalid("model ID is required")
	}
	if len(modelID) > otohaModelIDMaxLen {
		return otohaInvalid("model ID is longer than %d characters", otohaModelIDMaxLen)
	}
	if strings.ContainsAny(modelID, " \t\r\n") {
		return otohaInvalid("model ID cannot contain spaces")
	}
	return nil
}

func normalizeOtohaCatalogEntryInput(input OtohaCatalogEntryInput) (OtohaCatalogEntry, error) {
	entry := OtohaCatalogEntry{
		ModelID:     strings.TrimSpace(input.ModelID),
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Enabled:     input.Enabled,
		Tools:       input.Tools,
		Context:     input.Context,
		MaxOutput:   input.MaxOutput,
	}
	if err := validateOtohaModelID(entry.ModelID); err != nil {
		return entry, err
	}
	if entry.Name == "" {
		entry.Name = entry.ModelID
	}
	if len(entry.Name) > otohaNameMaxLen {
		return entry, otohaInvalid("name is longer than %d characters", otohaNameMaxLen)
	}
	if len(entry.Description) > otohaDescriptionMaxLen {
		return entry, otohaInvalid("description is longer than %d characters", otohaDescriptionMaxLen)
	}
	if entry.Context < 0 || entry.MaxOutput < 0 || entry.Context > otohaTokensMax || entry.MaxOutput > otohaTokensMax {
		return entry, otohaInvalid("context and longest reply must be between 0 and %d tokens", otohaTokensMax)
	}

	var err error
	if entry.Inputs, err = normalizeOtohaList(input.Inputs, otohaInputs, "input"); err != nil {
		return entry, err
	}
	if len(entry.Inputs) == 0 {
		entry.Inputs = []string{"text"}
	}
	if entry.Reasoning, err = normalizeOtohaReasoning(input.Reasoning); err != nil {
		return entry, err
	}
	entry.DefaultReasoning = strings.ToLower(strings.TrimSpace(input.DefaultReasoning))
	if entry.DefaultReasoning != "" && !otohaContains(entry.Reasoning, entry.DefaultReasoning) {
		return entry, otohaInvalid("default reasoning %q is not one of the model's reasoning levels", entry.DefaultReasoning)
	}
	if entry.Speed, err = normalizeOtohaChoice(input.Speed, otohaSpeeds, "speed"); err != nil {
		return entry, err
	}
	if entry.Complexity, err = normalizeOtohaChoice(input.Complexity, otohaComplexities, "complexity"); err != nil {
		return entry, err
	}
	if entry.ProfileSource, err = normalizeOtohaChoice(input.ProfileSource, otohaProfileSources, "profile source"); err != nil {
		return entry, err
	}
	if entry.CostTier, err = normalizeOtohaChoice(input.CostTier, otohaCostTiers, "price tier"); err != nil {
		return entry, err
	}
	if entry.API, err = normalizeOtohaChoice(input.API, otohaAPIs, "format"); err != nil {
		return entry, err
	}
	if entry.Roles, err = normalizeOtohaList(input.Roles, otohaRoles, "role"); err != nil {
		return entry, err
	}
	if entry.Use, err = normalizeOtohaList(input.Use, otohaUses, "use"); err != nil {
		return entry, err
	}
	if len(input.Strengths) > 0 {
		entry.Strengths = make(map[string]string, len(input.Strengths))
		for domain, level := range input.Strengths {
			domain = strings.ToLower(strings.TrimSpace(domain))
			level = strings.ToLower(strings.TrimSpace(level))
			if !otohaContains(otohaDomains, domain) {
				return entry, otohaInvalid("unknown domain %q", domain)
			}
			if level == "" {
				continue
			}
			if !otohaContains(otohaStrengthLevels, level) {
				return entry, otohaInvalid("unknown level %q for %s", level, domain)
			}
			entry.Strengths[domain] = level
		}
		if len(entry.Strengths) == 0 {
			entry.Strengths = nil
		}
	}

	return entry, nil
}

func normalizeOtohaChoice(value string, allowed []string, what string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || otohaContains(allowed, value) {
		return value, nil
	}
	return "", otohaInvalid("unknown %s %q", what, value)
}

// normalizeOtohaList keeps the allowed values once each, in the allowed list's order.
func normalizeOtohaList(values []string, allowed []string, what string) ([]string, error) {
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		if !otohaContains(allowed, v) {
			return nil, otohaInvalid("unknown %s %q", what, v)
		}
		seen[v] = true
	}
	if len(seen) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(seen))
	for _, v := range allowed {
		if seen[v] {
			out = append(out, v)
		}
	}
	return out, nil
}

// normalizeOtohaReasoning keeps the levels once each in the admin's order (lowest first).
func normalizeOtohaReasoning(values []string) ([]string, error) {
	var out []string
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || otohaContains(out, v) {
			continue
		}
		if !otohaReasoningPattern.MatchString(v) {
			return nil, otohaInvalid("reasoning level %q is not a single word", v)
		}
		out = append(out, v)
	}
	return out, nil
}

func otohaContains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

// ---- this period's usage ----

// OtohaUsagePeriod is the period a key's per-model usage covers: the plan's current monthly window when it has one,
// else the calendar month in the server's time zone.
func OtohaUsagePeriod(plan *UserSubscription, now time.Time) (time.Time, time.Time) {
	if plan != nil && plan.MonthlyWindowStart != nil {
		start := plan.windowResetAnchor(*plan.MonthlyWindowStart)
		if advanced, ok := plan.automaticWindowStartAt(plan.MonthlyWindowStart, 30*24*time.Hour, now); ok {
			start = advanced
		}
		end := start.Add(30 * 24 * time.Hour)
		if !plan.ExpiresAt.IsZero() && plan.ExpiresAt.After(start) && plan.ExpiresAt.Before(end) {
			end = plan.ExpiresAt
		}
		return start, end
	}
	start := timezone.StartOfMonth(now)
	return start, start.AddDate(0, 1, 0)
}

// OtohaModelUsageFromStats keeps, of each model's stats, the counts and what the user paid.
func OtohaModelUsageFromStats(stats []usagestats.ModelStat) []OtohaModelUsage {
	out := make([]OtohaModelUsage, 0, len(stats))
	for _, s := range stats {
		out = append(out, OtohaModelUsage{
			Model:               s.Model,
			Requests:            s.Requests,
			InputTokens:         s.InputTokens,
			OutputTokens:        s.OutputTokens,
			CacheCreationTokens: s.CacheCreationTokens,
			CacheReadTokens:     s.CacheReadTokens,
			TotalTokens:         s.TotalTokens,
			Cost:                s.ActualCost,
		})
	}
	return out
}
