package service

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Image models in the Otoha catalog (TASK-73). The app draws with `POST /v1/images/generations` and
// `/v1/images/edits` on the same key; billing charges a request per image by the size tier of the output (1K, 2K or
// 4K), from the group's per-model price, else the group's image prices, else the built-in defaults, times the
// group's image rate. The catalog lists these models apart from the chat models and prices each size the same way,
// so what the app shows is what is charged. The admin may set the price of a tier on the model itself; billing reads
// it from here (OtohaImagePriceOverrides) so the catalog and the charge cannot differ.

const (
	// OtohaImageAPI is the format the app calls an image model in: the OpenAI images endpoints, which the gateway
	// serves for OpenAI and Grok models alike.
	OtohaImageAPI = "openai-images"
	// OtohaImageAPIGemini is the format a Gemini picture model is reached in: the native generateContent call on
	// /v1beta/models/<model>:generateContent. The gateway's images endpoints answer 404 for a Gemini model, so the
	// OpenAI images format is not available for it, and the Otoha app, which draws with openai-images, cannot use it
	// (TASK-85).
	OtohaImageAPIGemini = "gemini-generate-content"

	// OtohaCatalogProblemImagesNotAllowed: the group does not allow image generation (allow_image_generation).
	OtohaCatalogProblemImagesNotAllowed = "images_not_allowed"

	otohaImagePer          = "image"
	otohaImageSizesMax     = 12
	otohaImageQualitiesMax = 8
	otohaImagePriceMax     = 1000.0
	otohaImageEdgeMin      = 16
	otohaImageEdgeMax      = 8192

	// What the usage log says was billed is read over this window and kept this long (TASK-77).
	otohaImageObservationDays = 30
	otohaImageObservationTTL  = 5 * time.Minute

	// OtohaImageBasisObserved: the size is priced at the tier recently billed for its output; OtohaImageBasisRequested:
	// no such record, so at the tier of the size itself.
	OtohaImageBasisObserved  = "observed"
	OtohaImageBasisRequested = "requested"
)

var (
	// otohaImageTiers are the size tiers billing charges, smallest first.
	otohaImageTiers = []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K}

	otohaImageSizePattern = regexp.MustCompile(`^([0-9]{2,5})x([0-9]{2,5})$`)
)

// OtohaImageEntry is one image model in a group's catalog as the admin edits it. Price1K, Price2K and Price4K are the
// price of one image in that size tier before the group's rate (what the group's own image prices mean); nil follows
// billing.
type OtohaImageEntry struct {
	ID          int64    `json:"id"`
	GroupID     int64    `json:"group_id"`
	ModelID     string   `json:"model_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
	SortOrder   int      `json:"sort_order"`
	Sizes       []string `json:"sizes"`
	Qualities   []string `json:"qualities"`
	Edits       bool     `json:"edits"`
	Price1K     *float64 `json:"price_1k"`
	Price2K     *float64 `json:"price_2k"`
	Price4K     *float64 `json:"price_4k"`
	// Source is OtohaImageSourceManual (the admin added it) or OtohaImageSourceAuto (made from the group's accounts,
	// TASK-76). ManualFields are the fields the admin changed on an auto entry, which a refresh leaves alone;
	// UnavailableSince is when the group's accounts stopped listing the model (auto entries only).
	Source           string     `json:"source"`
	ManualFields     []string   `json:"manual_fields"`
	UnavailableSince *time.Time `json:"unavailable_since"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// priceOf is the price the admin set for a size tier; nil when none.
func (e OtohaImageEntry) priceOf(tier string) *float64 {
	return OtohaImageTierPrices{P1K: e.Price1K, P2K: e.Price2K, P4K: e.Price4K}.For(tier)
}

// OtohaImageEntryInput is what the admin submits for an image model.
type OtohaImageEntryInput struct {
	ModelID     string
	Name        string
	Description string
	Enabled     bool
	Sizes       []string
	Qualities   []string
	Edits       bool
	Price1K     *float64
	Price2K     *float64
	Price4K     *float64
}

// OtohaImageTierPrices are the prices the admin set on an image model, per size tier, before the group's rate.
type OtohaImageTierPrices struct {
	P1K *float64
	P2K *float64
	P4K *float64
}

// For gives the price of a size tier; nil when not set.
func (p OtohaImageTierPrices) For(tier string) *float64 {
	switch tier {
	case ImageBillingSize1K:
		return p.P1K
	case ImageBillingSize2K:
		return p.P2K
	case ImageBillingSize4K:
		return p.P4K
	default:
		return nil
	}
}

func (p OtohaImageTierPrices) any() bool {
	return p.P1K != nil || p.P2K != nil || p.P4K != nil
}

// OtohaImageTierPrice is the price of one image in a size tier for the admin: what billing charges before the
// group's rate (Upstream) and after it (Sale); Set says the admin set it on the model.
type OtohaImageTierPrice struct {
	Tier     string  `json:"tier"`
	Upstream float64 `json:"upstream"`
	Sale     float64 `json:"sale"`
	Set      bool    `json:"set,omitempty"`
}

// OtohaImageSizeBasis tells at which tier a size of an image model is priced and why. Some upstreams draw a different
// size from the one asked for (1024x1024 comes back 1254x1254) and billing charges the tier of what was drawn, so a
// size is priced at the highest tier billed for it in the last days (Basis "observed", ObservedOutputSize is one
// output of that tier); with no such record, at the tier of the size itself (Basis "requested").
type OtohaImageSizeBasis struct {
	Size               string `json:"size"`
	RequestedTier      string `json:"requested_tier"`
	BilledTier         string `json:"billed_tier"`
	ObservedOutputSize string `json:"observed_output_size,omitempty"`
	Basis              string `json:"basis"`
}

// OtohaImageObservation is a size asked for (RequestedSize) of a model whose output billing charged at Tier, with an
// OutputSize drawn.
type OtohaImageObservation struct {
	Model         string
	RequestedSize string
	Tier          string
	OutputSize    string
}

// OtohaImageObservations is what an image repository can add when it can read the usage log: the tiers billed for
// the outputs of a group's image requests since a time, most recent first.
type OtohaImageObservations interface {
	ObservedImageOutputs(ctx context.Context, groupID int64, since time.Time) ([]OtohaImageObservation, error)
}

type otohaImageObservationKey struct{ model, size string }

// otohaImageObservationIndex holds the observations by model and requested size, in the order they came.
type otohaImageObservationIndex map[otohaImageObservationKey][]OtohaImageObservation

type otohaImageObservationCacheEntry struct {
	index     otohaImageObservationIndex
	expiresAt time.Time
}

func normalizeOtohaImageObservationSize(size string) string {
	return strings.ToLower(strings.TrimSpace(size))
}

func indexOtohaImageObservations(rows []OtohaImageObservation) otohaImageObservationIndex {
	index := otohaImageObservationIndex{}
	for _, row := range rows {
		if imageTierRank(row.Tier) == 0 {
			continue
		}
		key := otohaImageObservationKey{model: strings.TrimSpace(row.Model), size: normalizeOtohaImageObservationSize(row.RequestedSize)}
		row.Tier = strings.ToUpper(strings.TrimSpace(row.Tier))
		row.OutputSize = strings.TrimSpace(row.OutputSize)
		index[key] = append(index[key], row)
	}
	return index
}

// inferOtohaImageBilledTier decides the tier a requested size of a model is priced at: the highest tier among the
// outputs billed for it (the first one when several share it, the most recent when rows come newest first), else the
// tier of the requested size.
func inferOtohaImageBilledTier(model, size string, index otohaImageObservationIndex) OtohaImageSizeBasis {
	basis := OtohaImageSizeBasis{Size: size, Basis: OtohaImageBasisRequested}
	basis.RequestedTier, _ = ClassifyImageBillingTier(size)
	basis.BilledTier = basis.RequestedTier
	var best *OtohaImageObservation
	rows := index[otohaImageObservationKey{model: strings.TrimSpace(model), size: normalizeOtohaImageObservationSize(size)}]
	for i := range rows {
		if best == nil || imageTierRank(rows[i].Tier) > imageTierRank(best.Tier) {
			best = &rows[i]
		}
	}
	if best != nil {
		basis.BilledTier, basis.ObservedOutputSize, basis.Basis = best.Tier, best.OutputSize, OtohaImageBasisObserved
	}
	return basis
}

// OtohaImageAdminEntry is an image model with what the admin needs to judge it: the price of each size tier, the tier
// each size is priced at and why, and why the app does not get it (Problem), if it does not.
type OtohaImageAdminEntry struct {
	OtohaImageEntry
	SizeBasis     []OtohaImageSizeBasis `json:"size_basis"`
	Prices        []OtohaImageTierPrice `json:"prices"`
	InCatalog     bool                  `json:"in_catalog"`
	Problem       string                `json:"problem"`
	RoutePlatform string                `json:"route_platform"`
	// RouteAPI is the format the route names when it is not the OpenAI images one (OtohaImageAPIGemini); empty
	// otherwise.
	RouteAPI string `json:"route_api"`

	lookupFailed bool
}

// OtohaImageModel is an image model as the app receives it: the sizes it may ask for, each with its tier and the
// price of one image (after the group's image rate, in Currency), the qualities, and whether the model edits images
// or takes reference images (`/v1/images/edits`).
type OtohaImageModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	API         string `json:"api"`
	// Endpoint is the call an API key makes, and App whether the Otoha app can use the model (it draws with
	// openai-images only).
	Endpoint  string           `json:"endpoint"`
	App       bool             `json:"app"`
	Edits     bool             `json:"edits"`
	Qualities []string         `json:"qualities"`
	Currency  string           `json:"currency"`
	Per       string           `json:"per"`
	Sizes     []OtohaImageSize `json:"sizes"`
}

// OtohaImageSize is a size the app may ask for, the billing tier it falls in and the price of one image.
type OtohaImageSize struct {
	Size  string  `json:"size"`
	Tier  string  `json:"tier"`
	Price float64 `json:"price"`
}

// OtohaImageRepository stores the image models of the catalog.
type OtohaImageRepository interface {
	// ListImagesByGroup lists a group's image models by sort order, then ID.
	ListImagesByGroup(ctx context.Context, groupID int64) ([]OtohaImageEntry, error)
	CreateImage(ctx context.Context, entry *OtohaImageEntry) error
	UpdateImage(ctx context.Context, entry *OtohaImageEntry) error
	DeleteImage(ctx context.Context, id int64) error
}

// OtohaImagePricer gives the price of one image of a size tier as billing charges it in the group, before the group's
// rate; false when billing cannot price it per image (for instance a channel that charges image models by token).
type OtohaImagePricer interface {
	ImageUnitPrice(ctx context.Context, group *Group, modelID, tier string) (float64, bool)
}

// OtohaImageRouting tells where the group sends a request for an image model on the images endpoints.
type OtohaImageRouting interface {
	ImageRoute(ctx context.Context, group *Group, modelID string) OtohaModelRoute
}

// OtohaImagePriceOverrides is what billing asks for the prices the admin set on an image model.
type OtohaImagePriceOverrides interface {
	OtohaImagePriceOverrides(ctx context.Context, groupID int64, model string) (OtohaImageTierPrices, bool)
}

var _ OtohaImagePriceOverrides = (*OtohaCatalogService)(nil)

// otohaImageRate says which rate prices images: the group's own image rate when it has one, else the rate the owner
// pays (the group's, or a rate of their own), as billing's resolveImageRateMultiplier does.
type otohaImageRate struct {
	independent bool
	rate        float64
}

func otohaImageRateOf(group *Group) otohaImageRate {
	if group == nil || !group.ImageRateIndependent {
		return otohaImageRate{}
	}
	return otohaImageRate{independent: true, rate: group.ImageRateMultiplier}
}

// at is the rate images are charged at when the owner pays rate.
func (r otohaImageRate) at(rate float64) float64 {
	if r.independent {
		rate = r.rate
	}
	if rate < 0 {
		return 0
	}
	return rate
}

// otohaImageItem is an image model that passes into the app's catalog, with its price per tier before any rate.
type otohaImageItem struct {
	entry    OtohaImageEntry
	upstream map[string]float64
	// tiers is the tier each size is priced at (OtohaImageSizeBasis.BilledTier).
	tiers map[string]string
	// api is the format the model is reached in (OtohaImageAPI when the route does not say).
	api string
}

type otohaImageOverrideCacheEntry struct {
	byModel   map[string]OtohaImageTierPrices
	expiresAt time.Time
}

// WithImages gives the service its image models: where they are stored, how billing prices an image, and where the
// group sends them. Without it the catalog has no image models.
func (s *OtohaCatalogService) WithImages(repo OtohaImageRepository, pricer OtohaImagePricer, routing OtohaImageRouting) *OtohaCatalogService {
	s.images, s.imagePricer, s.imageRouting = repo, pricer, routing
	return s
}

func (s *OtohaCatalogService) listImages(ctx context.Context, groupID int64) ([]OtohaImageEntry, error) {
	if s.images == nil {
		return nil, nil
	}
	return s.images.ListImagesByGroup(ctx, groupID)
}

// OtohaImagePriceOverrides gives the prices the admin set on the group's image model, for billing; false when the
// model has no entry or the entry sets no price. It reads a short-lived copy, and an edit through this service shows
// at once.
func (s *OtohaCatalogService) OtohaImagePriceOverrides(ctx context.Context, groupID int64, model string) (OtohaImageTierPrices, bool) {
	if s == nil || s.images == nil || groupID <= 0 {
		return OtohaImageTierPrices{}, false
	}
	now := s.now()
	s.cacheMu.Lock()
	cached, ok := s.imageOverrides[groupID]
	s.cacheMu.Unlock()
	if !ok || !now.Before(cached.expiresAt) {
		entries, err := s.images.ListImagesByGroup(ctx, groupID)
		if err != nil {
			return OtohaImageTierPrices{}, false
		}
		cached = otohaImageOverrideCacheEntry{byModel: map[string]OtohaImageTierPrices{}, expiresAt: now.Add(otohaCatalogCacheTTL)}
		for _, entry := range entries {
			prices := OtohaImageTierPrices{P1K: entry.Price1K, P2K: entry.Price2K, P4K: entry.Price4K}
			if prices.any() {
				cached.byModel[entry.ModelID] = prices
			}
		}
		s.cacheMu.Lock()
		s.imageOverrides[groupID] = cached
		s.cacheMu.Unlock()
	}
	prices, found := cached.byModel[strings.TrimSpace(model)]
	return prices, found
}

// CreateImageEntry adds an image model to the end of a group's catalog.
func (s *OtohaCatalogService) CreateImageEntry(ctx context.Context, groupID int64, input OtohaImageEntryInput) (*OtohaImageEntry, error) {
	if _, err := s.groups.GetByIDLite(ctx, groupID); err != nil {
		return nil, err
	}
	entry, err := normalizeOtohaImageEntryInput(input)
	if err != nil {
		return nil, err
	}
	existing, err := s.listImages(ctx, groupID)
	if err != nil {
		return nil, err
	}
	entry.GroupID = groupID
	entry.Source = OtohaImageSourceManual
	entry.SortOrder = otohaSortOrderStep
	for _, e := range existing {
		if e.SortOrder+otohaSortOrderStep > entry.SortOrder {
			entry.SortOrder = e.SortOrder + otohaSortOrderStep
		}
	}
	if err := s.images.CreateImage(ctx, &entry); err != nil {
		return nil, err
	}
	s.InvalidateGroup(groupID)
	return &entry, nil
}

// UpdateImageEntry replaces an image model of the group; its place in the order stays.
func (s *OtohaCatalogService) UpdateImageEntry(ctx context.Context, groupID, entryID int64, input OtohaImageEntryInput) (*OtohaImageEntry, error) {
	current, err := s.groupImageEntry(ctx, groupID, entryID)
	if err != nil {
		return nil, err
	}
	entry, err := normalizeOtohaImageEntryInput(input)
	if err != nil {
		return nil, err
	}
	entry.ID = current.ID
	entry.GroupID = current.GroupID
	entry.SortOrder = current.SortOrder
	entry.CreatedAt = current.CreatedAt
	entry.Source = current.Source
	entry.UnavailableSince = current.UnavailableSince
	if current.Source == OtohaImageSourceAuto {
		// What the admin changes stays theirs: a later check of the accounts refreshes only the rest.
		entry.ManualFields = append([]string{}, current.ManualFields...)
		for _, field := range changedOtohaImageFields(*current, entry) {
			if !otohaContains(entry.ManualFields, field) {
				entry.ManualFields = append(entry.ManualFields, field)
			}
		}
	}
	if err := s.images.UpdateImage(ctx, &entry); err != nil {
		return nil, err
	}
	s.InvalidateGroup(groupID)
	return &entry, nil
}

// DeleteImageEntry removes an image model of the group.
func (s *OtohaCatalogService) DeleteImageEntry(ctx context.Context, groupID, entryID int64) error {
	if _, err := s.groupImageEntry(ctx, groupID, entryID); err != nil {
		return err
	}
	if err := s.images.DeleteImage(ctx, entryID); err != nil {
		return err
	}
	s.InvalidateGroup(groupID)
	return nil
}

func (s *OtohaCatalogService) groupImageEntry(ctx context.Context, groupID, entryID int64) (*OtohaImageEntry, error) {
	entries, err := s.listImages(ctx, groupID)
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

// evaluateImages prices and checks every image model for the admin, and gives those that pass into the app's catalog.
func (s *OtohaCatalogService) evaluateImages(ctx context.Context, group *Group, entries []OtohaImageEntry) ([]OtohaImageAdminEntry, []otohaImageItem) {
	out := make([]OtohaImageAdminEntry, 0, len(entries))
	items := make([]otohaImageItem, 0, len(entries))
	imageRate := otohaImageRateOf(group).at(group.RateMultiplier)
	observations, observationsRead := s.imageObservations(ctx, group.ID, len(entries) > 0)
	for _, entry := range entries {
		if entry.ManualFields == nil {
			entry.ManualFields = []string{}
		}
		item := OtohaImageAdminEntry{OtohaImageEntry: entry, Prices: []OtohaImageTierPrice{}, SizeBasis: []OtohaImageSizeBasis{}}
		route := OtohaModelRoute{}
		if s.imageRouting != nil {
			route = s.imageRouting.ImageRoute(ctx, group, entry.ModelID)
		}
		item.RoutePlatform = route.Platform
		if route.API != OtohaImageAPI {
			item.RouteAPI = route.API
		}
		item.lookupFailed = route.Failed || !observationsRead

		upstream := map[string]float64{}
		for _, tier := range otohaImageTiers {
			price, ok := s.imageTierPrice(ctx, group, entry, route, tier)
			if !ok {
				continue
			}
			upstream[tier] = price
			item.Prices = append(item.Prices, OtohaImageTierPrice{
				Tier:     tier,
				Upstream: roundOtohaImagePrice(price),
				Sale:     roundOtohaImagePrice(price * imageRate),
				Set:      entry.priceOf(tier) != nil,
			})
		}
		priced := true
		tiers := map[string]string{}
		for _, size := range entry.Sizes {
			basis := inferOtohaImageBilledTier(entry.ModelID, size, observations)
			item.SizeBasis = append(item.SizeBasis, basis)
			tiers[size] = basis.BilledTier
			if basis.BilledTier == "" {
				priced = false
			} else if _, has := upstream[basis.BilledTier]; !has {
				priced = false
			}
		}
		switch {
		case !entry.Enabled:
			item.Problem = OtohaCatalogProblemDisabled
		case entry.UnavailableSince != nil:
			item.Problem = OtohaCatalogProblemUpstreamMissing
		case group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(entry.ModelID):
			item.Problem = OtohaCatalogProblemNotAllowed
		case !group.AllowImageGeneration:
			item.Problem = OtohaCatalogProblemImagesNotAllowed
		case route.Problem != "":
			item.Problem = route.Problem
		case !priced:
			item.Problem = OtohaCatalogProblemNoPrice
		default:
			item.InCatalog = true
			items = append(items, otohaImageItem{entry: entry, upstream: upstream, tiers: tiers, api: route.API})
		}
		out = append(out, item)
	}
	return out, items
}

// imageObservations reads which tiers billing charged for the outputs of the group's image requests in the last days,
// kept for a few minutes; false when the usage log could not be read (the sizes are then priced by their own size and
// the catalog is not remembered). A repository that cannot read it gives none.
func (s *OtohaCatalogService) imageObservations(ctx context.Context, groupID int64, needed bool) (otohaImageObservationIndex, bool) {
	observer, ok := s.images.(OtohaImageObservations)
	if !needed || !ok {
		return otohaImageObservationIndex{}, true
	}
	now := s.now()
	s.cacheMu.Lock()
	cached, found := s.imageObs[groupID]
	s.cacheMu.Unlock()
	if found && now.Before(cached.expiresAt) {
		return cached.index, true
	}
	rows, err := observer.ObservedImageOutputs(ctx, groupID, now.AddDate(0, 0, -otohaImageObservationDays))
	if err != nil {
		logger.L().Warn("otoha_catalog.billed_image_tiers_failed", zap.Int64("group_id", groupID), zap.Error(err))
		return otohaImageObservationIndex{}, false
	}
	index := indexOtohaImageObservations(rows)
	s.cacheMu.Lock()
	s.imageObs[groupID] = otohaImageObservationCacheEntry{index: index, expiresAt: now.Add(otohaImageObservationTTL)}
	s.cacheMu.Unlock()
	return index, true
}

// imageTierPrice is the price of one image of a tier before the group's rate: the admin's on the model, else what
// billing charges for the model as routed.
func (s *OtohaCatalogService) imageTierPrice(ctx context.Context, group *Group, entry OtohaImageEntry, route OtohaModelRoute, tier string) (float64, bool) {
	if set := entry.priceOf(tier); set != nil {
		return *set, true
	}
	if s.imagePricer == nil {
		return 0, false
	}
	routedCtx := ctx
	if route.Platform != "" {
		routedCtx = WithCompositeRouteDecision(ctx, CompositeRouteDecision{
			Matched:        true,
			PublicModel:    entry.ModelID,
			TargetPlatform: route.Platform,
			UpstreamModel:  route.forwardedModel(entry.ModelID),
			Endpoint:       CompositeRouteEndpointImages,
		})
	}
	return s.imagePricer.ImageUnitPrice(routedCtx, group, route.forwardedModel(entry.ModelID), tier)
}

// buildOtohaImageModels prices the catalog's image models at the image rate; never nil.
func buildOtohaImageModels(items []otohaImageItem, rate float64) []OtohaImageModel {
	models := make([]OtohaImageModel, 0, len(items))
	for _, item := range items {
		entry := item.entry
		api := item.api
		if api == "" {
			api = OtohaImageAPI
		}
		endpoint, app := otohaImageAPIEndpoint(api, entry.ModelID)
		model := OtohaImageModel{
			ID:          entry.ModelID,
			Name:        entry.Name,
			Description: entry.Description,
			API:         api,
			Endpoint:    endpoint,
			App:         app,
			Edits:       entry.Edits,
			Qualities:   append([]string{}, entry.Qualities...),
			Currency:    otohaPriceCurrency,
			Per:         otohaImagePer,
			Sizes:       make([]OtohaImageSize, 0, len(entry.Sizes)),
		}
		if model.Name == "" {
			model.Name = entry.ModelID
		}
		for _, size := range entry.Sizes {
			tier := item.tiers[size]
			model.Sizes = append(model.Sizes, OtohaImageSize{Size: size, Tier: tier, Price: roundOtohaImagePrice(item.upstream[tier] * rate)})
		}
		models = append(models, model)
	}
	return models
}

// otohaImageAPIEndpoint is the call an API key makes for a picture model in a format, and whether the Otoha app can
// use it.
func otohaImageAPIEndpoint(api, modelID string) (string, bool) {
	if api == OtohaImageAPIGemini {
		return "POST /v1beta/models/" + modelID + ":generateContent", false
	}
	return "POST /v1/images/generations", true
}

func roundOtohaImagePrice(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// ---- validation ----

func normalizeOtohaImageEntryInput(input OtohaImageEntryInput) (OtohaImageEntry, error) {
	entry := OtohaImageEntry{
		ModelID:     strings.TrimSpace(input.ModelID),
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Enabled:     input.Enabled,
		Edits:       input.Edits,
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
	var err error
	if entry.Sizes, err = normalizeOtohaImageSizes(input.Sizes); err != nil {
		return entry, err
	}
	if entry.Qualities, err = normalizeOtohaImageQualities(input.Qualities); err != nil {
		return entry, err
	}
	if entry.Price1K, err = normalizeOtohaImagePrice(input.Price1K); err != nil {
		return entry, err
	}
	if entry.Price2K, err = normalizeOtohaImagePrice(input.Price2K); err != nil {
		return entry, err
	}
	if entry.Price4K, err = normalizeOtohaImagePrice(input.Price4K); err != nil {
		return entry, err
	}
	return entry, nil
}

// normalizeOtohaImageSizes keeps the sizes once each, as WIDTHxHEIGHT in lower case, in the admin's order. At least
// one is needed, since the app asks for one of them.
func normalizeOtohaImageSizes(values []string) ([]string, error) {
	out := []string{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || otohaContains(out, v) {
			continue
		}
		match := otohaImageSizePattern.FindStringSubmatch(v)
		if match == nil {
			return nil, otohaInvalid("size %q must be WIDTHxHEIGHT, for example 1024x1024", v)
		}
		for _, edge := range match[1:] {
			n, _ := strconv.Atoi(edge)
			if n < otohaImageEdgeMin || n > otohaImageEdgeMax {
				return nil, otohaInvalid("size %q must have sides between %d and %d pixels", v, otohaImageEdgeMin, otohaImageEdgeMax)
			}
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, otohaInvalid("list at least one size the app may ask for")
	}
	if len(out) > otohaImageSizesMax {
		return nil, otohaInvalid("at most %d sizes", otohaImageSizesMax)
	}
	return out, nil
}

// normalizeOtohaImageQualities keeps the qualities once each in the admin's order, as single lower-case words.
func normalizeOtohaImageQualities(values []string) ([]string, error) {
	out := []string{}
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || otohaContains(out, v) {
			continue
		}
		if !otohaReasoningPattern.MatchString(v) {
			return nil, otohaInvalid("quality %q is not a single word", v)
		}
		out = append(out, v)
	}
	if len(out) > otohaImageQualitiesMax {
		return nil, otohaInvalid("at most %d qualities", otohaImageQualitiesMax)
	}
	return out, nil
}

func normalizeOtohaImagePrice(price *float64) (*float64, error) {
	if price == nil {
		return nil, nil
	}
	if math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 || *price > otohaImagePriceMax {
		return nil, otohaInvalid("an image price must be between 0 and %s", fmt.Sprint(otohaImagePriceMax))
	}
	rounded := roundOtohaImagePrice(*price)
	return &rounded, nil
}
