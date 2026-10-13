package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
)

// Video, voice and quick-judgement models in the Otoha catalog (TASK-81). The Otoha app cannot use them yet; the
// catalog lists them so the account page can show what an API key reaches and what it costs.
//
// Each kind has its own array in the catalog (videoModels, voiceModels, quickModels), as images have imageModels:
// their prices have different shapes (per second by resolution, per voice unit, per million input tokens), so one
// array with a kind field would need most fields to be optional. Existing keys are unchanged, so an app that reads
// only models and imageModels reads the catalog as before.
//
// Every price is what billing charges before the rate, taken from billing itself (OtohaMediaPricer, the token price
// resolver), times the rate billing applies to that kind: the video rate for video (the group's own when it has one),
// the key owner's group rate for voice and for tokens.

const (
	OtohaMediaKindVideo = "video"
	OtohaMediaKindVoice = "voice"
	OtohaMediaKindQuick = "quick"

	// The voice functions the gateway forwards and bills (grok_audio.go): the model of the usage log is the endpoint.
	OtohaVoiceModeTTS = "tts"
	OtohaVoiceModeSTT = "stt"

	// The formats the endpoints speak.
	OtohaVideoAPI = "grok-videos"
	OtohaVoiceAPI = "grok-voice"
	OtohaQuickAPI = "typesafe-systemone"

	otohaVideoEndpoint = "POST /v1/videos/generations"
	otohaQuickEndpoint = "POST /v1/systemone"

	otohaVideoPer      = "second"
	otohaVoicePerTTS   = "1M characters"
	otohaVoicePerSTT   = "hour"
	otohaQuickBilledIn = "input"

	otohaMediaReportKeyPrefix = "otoha_media_discovery_"
	otohaMediaEntriesKey      = "otoha_media_models_"
)

var ErrOtohaMediaDiscoveryUnavailable = infraerrors.New(http.StatusServiceUnavailable, "OTOHA_MEDIA_UNAVAILABLE", "video, voice and quick-judgement models are not available")

// otohaVideoResolutions are the resolutions billing prices, smallest first.
var otohaVideoResolutions = []string{VideoBillingResolution480P, VideoBillingResolution720P, VideoBillingResolution1080P}

// OtohaMediaEntry is one video, voice or quick-judgement model of a group's catalog as the admin sees it. Entries
// are made from the group's accounts; the admin may switch one off, rename it and describe it, and what was changed
// is kept in ManualFields so a later check leaves it alone. UnavailableSince is when the group's accounts stopped
// offering the model.
type OtohaMediaEntry struct {
	Kind             string     `json:"kind"`
	ModelID          string     `json:"model_id"`
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	Enabled          bool       `json:"enabled"`
	SortOrder        int        `json:"sort_order"`
	ManualFields     []string   `json:"manual_fields"`
	UnavailableSince *time.Time `json:"unavailable_since"`
}

// OtohaMediaEntryInput is what the admin submits for an entry: which one, and the fields that can change.
type OtohaMediaEntryInput struct {
	Kind        string
	ModelID     string
	Name        string
	Description string
	Enabled     bool
}

// The fields of a media entry the admin can change.
const (
	otohaMediaFieldName        = "name"
	otohaMediaFieldDescription = "description"
	otohaMediaFieldEnabled     = "enabled"
)

// OtohaVideoModel is a video model as the catalog lists it: the price of one second of video by resolution (after
// the video rate, in Currency).
type OtohaVideoModel struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	API         string                 `json:"api"`
	Endpoint    string                 `json:"endpoint"`
	App         bool                   `json:"app"`
	Currency    string                 `json:"currency"`
	Per         string                 `json:"per"`
	Resolutions []OtohaVideoResolution `json:"resolutions"`
}

// OtohaVideoResolution is a resolution and the price of one second of video in it.
type OtohaVideoResolution struct {
	Resolution string  `json:"resolution"`
	Price      float64 `json:"price"`
}

// OtohaVoiceModel is a voice function as the catalog lists it: Mode "tts" is priced per million characters of text,
// "stt" per hour of audio, as billing counts them.
type OtohaVoiceModel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	API         string  `json:"api"`
	Endpoint    string  `json:"endpoint"`
	App         bool    `json:"app"`
	Mode        string  `json:"mode"`
	Currency    string  `json:"currency"`
	Per         string  `json:"per"`
	Price       float64 `json:"price"`
}

// OtohaQuickModel is a quick-judgement model (TypeSafe Jev): billed on input tokens only, so Price is per million
// input tokens. OutputPrice is set only when billing also charges output tokens for it.
type OtohaQuickModel struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	API         string  `json:"api"`
	Endpoint    string  `json:"endpoint"`
	App         bool    `json:"app"`
	Currency    string  `json:"currency"`
	Per         string  `json:"per"`
	Billed      string  `json:"billed"`
	Price       float64 `json:"price"`
	OutputPrice float64 `json:"outputPrice,omitempty"`
}

// OtohaMediaAdminPrice is a price of an entry for the admin: what billing charges before the rate and after it. Label
// is the resolution (video), the voice mode, or "input" / "output" (quick).
type OtohaMediaAdminPrice struct {
	Label    string  `json:"label"`
	Upstream float64 `json:"upstream"`
	Sale     float64 `json:"sale"`
}

// OtohaMediaAdminEntry is an entry with what the admin needs to judge it: its prices, the unit they are per, the
// endpoint the app would call, and why the catalog does not list it (Problem), if it does not.
type OtohaMediaAdminEntry struct {
	OtohaMediaEntry
	Endpoint      string                 `json:"endpoint"`
	Per           string                 `json:"per"`
	Prices        []OtohaMediaAdminPrice `json:"prices"`
	InCatalog     bool                   `json:"in_catalog"`
	Problem       string                 `json:"problem"`
	RoutePlatform string                 `json:"route_platform"`

	lookupFailed bool
}

// OtohaMediaStore keeps a group's media entries.
type OtohaMediaStore interface {
	LoadMediaEntries(ctx context.Context, groupID int64) ([]OtohaMediaEntry, error)
	SaveMediaEntries(ctx context.Context, groupID int64, entries []OtohaMediaEntry) error
}

// OtohaMediaPricer gives what billing charges before the rate: one second of a video model at a resolution, and one
// unit of a voice function (a million characters of text for "tts", an hour of audio for "stt"); false when billing
// cannot price it that way (a channel that charges it per request or by token).
type OtohaMediaPricer interface {
	VideoSecondPrice(ctx context.Context, group *Group, modelID, resolution string) (float64, bool)
	VoiceUnitPrice(ctx context.Context, group *Group, mode string) (float64, bool)
}

// OtohaMediaRouting tells whether the group can serve a model of a kind on the endpoint the app would call.
type OtohaMediaRouting interface {
	MediaRoute(ctx context.Context, group *Group, kind, modelID string) OtohaModelRoute
}

type otohaMediaState struct {
	store   OtohaMediaStore
	pricer  OtohaMediaPricer
	routing OtohaMediaRouting
	// mu serializes changes to a group's entries: a check of the accounts and an admin's edit both read, change and
	// write them back.
	mu sync.Mutex

	accounts OtohaMediaAccountSource
	reports  OtohaMediaDiscoveryStore
}

// WithMedia gives the service its video, voice and quick-judgement models: where they are kept, how billing prices
// them, and where the group sends them. Without it the catalog has none.
func (s *OtohaCatalogService) WithMedia(store OtohaMediaStore, pricer OtohaMediaPricer, routing OtohaMediaRouting) *OtohaCatalogService {
	if s.media == nil {
		s.media = &otohaMediaState{}
	}
	s.media.store, s.media.pricer, s.media.routing = store, pricer, routing
	return s
}

func (s *OtohaCatalogService) listMedia(ctx context.Context, groupID int64) ([]OtohaMediaEntry, error) {
	if s.media == nil || s.media.store == nil {
		return nil, nil
	}
	return s.media.store.LoadMediaEntries(ctx, groupID)
}

func otohaMediaKnownKind(kind string) bool {
	return kind == OtohaMediaKindVideo || kind == OtohaMediaKindVoice || kind == OtohaMediaKindQuick
}

// UpdateMediaEntry changes the name, description and switch of an entry; what differs from the entry before is
// remembered as the admin's, so a later check of the accounts leaves it.
func (s *OtohaCatalogService) UpdateMediaEntry(ctx context.Context, groupID int64, input OtohaMediaEntryInput) (*OtohaMediaEntry, error) {
	if s == nil || s.media == nil || s.media.store == nil {
		return nil, ErrOtohaMediaDiscoveryUnavailable
	}
	input.Name, input.Description = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description)
	if len(input.Name) > otohaNameMaxLen {
		return nil, otohaInvalid("name is longer than %d characters", otohaNameMaxLen)
	}
	if len(input.Description) > otohaDescriptionMaxLen {
		return nil, otohaInvalid("description is longer than %d characters", otohaDescriptionMaxLen)
	}
	s.media.mu.Lock()
	defer s.media.mu.Unlock()
	entries, err := s.media.store.LoadMediaEntries(ctx, groupID)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		current := entries[i]
		if current.Kind != input.Kind || current.ModelID != strings.TrimSpace(input.ModelID) {
			continue
		}
		updated := current
		updated.Name = input.Name
		if updated.Name == "" {
			updated.Name = current.ModelID
		}
		updated.Description, updated.Enabled = input.Description, input.Enabled
		updated.ManualFields = append([]string{}, current.ManualFields...)
		for _, field := range changedOtohaMediaFields(current, updated) {
			if !otohaContains(updated.ManualFields, field) {
				updated.ManualFields = append(updated.ManualFields, field)
			}
		}
		entries[i] = updated
		if err := s.media.store.SaveMediaEntries(ctx, groupID, entries); err != nil {
			return nil, err
		}
		s.InvalidateGroup(groupID)
		return &updated, nil
	}
	return nil, ErrOtohaCatalogEntryNotFound
}

func changedOtohaMediaFields(before, after OtohaMediaEntry) []string {
	changed := []string{}
	if before.Name != after.Name {
		changed = append(changed, otohaMediaFieldName)
	}
	if before.Description != after.Description {
		changed = append(changed, otohaMediaFieldDescription)
	}
	if before.Enabled != after.Enabled {
		changed = append(changed, otohaMediaFieldEnabled)
	}
	return changed
}

// ---- what the app reads ----

// otohaMediaItem is an entry that passes into the app's catalog, with its prices before any rate.
type otohaMediaItem struct {
	entry OtohaMediaEntry
	// resolutions are the priced resolutions of a video model, smallest first.
	resolutions []string
	prices      map[string]float64
	// input and output are the token prices of a quick model, per million tokens.
	input, output float64
}

// otohaMediaItems are the entries that pass into the catalog, by kind, with the rate each kind is priced at.
type otohaMediaItems struct {
	video, voice, quick []otohaMediaItem
	videoRate           otohaImageRate
}

func (m otohaMediaItems) any() bool {
	return len(m.video)+len(m.voice)+len(m.quick) > 0
}

func otohaVideoRateOf(group *Group) otohaImageRate {
	if group == nil || !group.VideoRateIndependent {
		return otohaImageRate{}
	}
	return otohaImageRate{independent: true, rate: group.VideoRateMultiplier}
}

// otohaVideoResolutionsOf are the resolutions a video model offers: the 1.5 family has 1080p, the others stop at
// 720p. This follows the video prices billing carries (defaultGrokImagineVideo15Price1080P and the 480p and 720p
// prices of the first family); an upstream list that names the resolutions is not read.
func otohaVideoResolutionsOf(modelID string) []string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), "grok-imagine-video-1.5") {
		return otohaVideoResolutions
	}
	return otohaVideoResolutions[:2]
}

func otohaMediaEndpoint(entry OtohaMediaEntry) string {
	switch entry.Kind {
	case OtohaMediaKindVideo:
		return otohaVideoEndpoint
	case OtohaMediaKindVoice:
		return "POST /v1/" + entry.ModelID
	default:
		return otohaQuickEndpoint
	}
}

func otohaMediaPer(entry OtohaMediaEntry) string {
	switch entry.Kind {
	case OtohaMediaKindVideo:
		return otohaVideoPer
	case OtohaMediaKindVoice:
		if entry.ModelID == OtohaVoiceModeSTT {
			return otohaVoicePerSTT
		}
		return otohaVoicePerTTS
	default:
		return otohaPricePer
	}
}

// evaluateMedia prices and checks every media entry for the admin, and gives those that pass into the app's catalog.
func (s *OtohaCatalogService) evaluateMedia(ctx context.Context, group *Group, entries []OtohaMediaEntry) ([]OtohaMediaAdminEntry, otohaMediaItems) {
	out := make([]OtohaMediaAdminEntry, 0, len(entries))
	items := otohaMediaItems{videoRate: otohaVideoRateOf(group)}
	for _, entry := range entries {
		if entry.ManualFields == nil {
			entry.ManualFields = []string{}
		}
		admin := OtohaMediaAdminEntry{
			OtohaMediaEntry: entry, Endpoint: otohaMediaEndpoint(entry), Per: otohaMediaPer(entry), Prices: []OtohaMediaAdminPrice{},
		}
		route := OtohaModelRoute{}
		if s.media != nil && s.media.routing != nil {
			route = s.media.routing.MediaRoute(ctx, group, entry.Kind, entry.ModelID)
		}
		admin.RoutePlatform = route.Platform
		admin.lookupFailed = route.Failed

		item := otohaMediaItem{entry: entry, prices: map[string]float64{}}
		rate := group.RateMultiplier
		switch entry.Kind {
		case OtohaMediaKindVideo:
			rate = items.videoRate.at(group.RateMultiplier)
			for _, resolution := range otohaVideoResolutionsOf(entry.ModelID) {
				if price, ok := s.mediaVideoPrice(ctx, group, entry.ModelID, resolution); ok {
					item.resolutions = append(item.resolutions, resolution)
					item.prices[resolution] = price
					admin.Prices = append(admin.Prices, OtohaMediaAdminPrice{Label: resolution, Upstream: roundOtohaImagePrice(price), Sale: roundOtohaImagePrice(price * rate)})
				}
			}
		case OtohaMediaKindVoice:
			if price, ok := s.mediaVoicePrice(ctx, group, entry.ModelID); ok {
				item.prices[entry.ModelID] = price
				admin.Prices = append(admin.Prices, OtohaMediaAdminPrice{Label: entry.ModelID, Upstream: roundOtohaImagePrice(price), Sale: roundOtohaImagePrice(price * rate)})
			}
		case OtohaMediaKindQuick:
			if price, _, _, ok := s.routedPrice(ctx, group, entry.ModelID, route); ok {
				item.input, item.output = price.Input, price.Output
				admin.Prices = append(admin.Prices, OtohaMediaAdminPrice{Label: otohaQuickBilledIn, Upstream: roundOtohaPrice(price.Input), Sale: roundOtohaPrice(price.Input * rate)})
				if price.Output > 0 {
					admin.Prices = append(admin.Prices, OtohaMediaAdminPrice{Label: "output", Upstream: roundOtohaPrice(price.Output), Sale: roundOtohaPrice(price.Output * rate)})
				}
			}
		}
		priced := len(admin.Prices) > 0
		switch {
		case !entry.Enabled:
			admin.Problem = OtohaCatalogProblemDisabled
		case entry.UnavailableSince != nil:
			admin.Problem = OtohaCatalogProblemUpstreamMissing
		case entry.Kind != OtohaMediaKindVoice && group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(entry.ModelID):
			admin.Problem = OtohaCatalogProblemNotAllowed
		case route.Problem != "":
			admin.Problem = route.Problem
		case !priced:
			admin.Problem = OtohaCatalogProblemNoPrice
		default:
			admin.InCatalog = true
			switch entry.Kind {
			case OtohaMediaKindVideo:
				items.video = append(items.video, item)
			case OtohaMediaKindVoice:
				items.voice = append(items.voice, item)
			default:
				items.quick = append(items.quick, item)
			}
		}
		out = append(out, admin)
	}
	return out, items
}

func (s *OtohaCatalogService) mediaVideoPrice(ctx context.Context, group *Group, modelID, resolution string) (float64, bool) {
	if s.media == nil || s.media.pricer == nil {
		return 0, false
	}
	return s.media.pricer.VideoSecondPrice(ctx, group, modelID, resolution)
}

func (s *OtohaCatalogService) mediaVoicePrice(ctx context.Context, group *Group, mode string) (float64, bool) {
	if s.media == nil || s.media.pricer == nil {
		return 0, false
	}
	return s.media.pricer.VoiceUnitPrice(ctx, group, mode)
}

// buildOtohaMediaModels prices the media models; the lists are never nil.
func buildOtohaMediaModels(catalog *OtohaCatalog, media otohaMediaItems, rate float64) {
	catalog.VideoModels = make([]OtohaVideoModel, 0, len(media.video))
	catalog.VoiceModels = make([]OtohaVoiceModel, 0, len(media.voice))
	catalog.QuickModels = make([]OtohaQuickModel, 0, len(media.quick))
	videoRate := media.videoRate.at(rate)
	for _, item := range media.video {
		model := OtohaVideoModel{
			ID: item.entry.ModelID, Name: otohaMediaName(item.entry), Description: item.entry.Description,
			API: OtohaVideoAPI, Endpoint: otohaVideoEndpoint, Currency: otohaPriceCurrency, Per: otohaVideoPer,
			Resolutions: make([]OtohaVideoResolution, 0, len(item.resolutions)),
		}
		for _, resolution := range item.resolutions {
			model.Resolutions = append(model.Resolutions, OtohaVideoResolution{Resolution: resolution, Price: roundOtohaImagePrice(item.prices[resolution] * videoRate)})
		}
		catalog.VideoModels = append(catalog.VideoModels, model)
	}
	for _, item := range media.voice {
		catalog.VoiceModels = append(catalog.VoiceModels, OtohaVoiceModel{
			ID: item.entry.ModelID, Name: otohaMediaName(item.entry), Description: item.entry.Description,
			API: OtohaVoiceAPI, Endpoint: otohaMediaEndpoint(item.entry), Mode: item.entry.ModelID,
			Currency: otohaPriceCurrency, Per: otohaMediaPer(item.entry), Price: roundOtohaImagePrice(item.prices[item.entry.ModelID] * rate),
		})
	}
	for _, item := range media.quick {
		catalog.QuickModels = append(catalog.QuickModels, OtohaQuickModel{
			ID: item.entry.ModelID, Name: otohaMediaName(item.entry), Description: item.entry.Description,
			API: OtohaQuickAPI, Endpoint: otohaQuickEndpoint, Currency: otohaPriceCurrency, Per: otohaPricePer, Billed: otohaQuickBilledIn,
			Price: roundOtohaPrice(item.input * rate), OutputPrice: roundOtohaPrice(item.output * rate),
		})
	}
}

func otohaMediaName(entry OtohaMediaEntry) string {
	if entry.Name != "" {
		return entry.Name
	}
	return entry.ModelID
}

// ---- keeping the entries ----

// settingOtohaMediaStore keeps a group's media entries in the settings, as one document: a group has a handful.
type settingOtohaMediaStore struct {
	settings SettingRepository
}

// NewOtohaMediaStore keeps each group's media entries in the settings.
func NewOtohaMediaStore(settings SettingRepository) OtohaMediaStore {
	return &settingOtohaMediaStore{settings: settings}
}

func otohaMediaEntriesKeyOf(groupID int64) string {
	return otohaMediaEntriesKey + strconv.FormatInt(groupID, 10)
}

type otohaMediaDocument struct {
	Entries []OtohaMediaEntry `json:"entries"`
}

func (s *settingOtohaMediaStore) LoadMediaEntries(ctx context.Context, groupID int64) ([]OtohaMediaEntry, error) {
	value, err := s.settings.GetValue(ctx, otohaMediaEntriesKeyOf(groupID))
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return []OtohaMediaEntry{}, nil
		}
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return []OtohaMediaEntry{}, nil
	}
	var doc otohaMediaDocument
	if err := json.Unmarshal([]byte(value), &doc); err != nil {
		return nil, fmt.Errorf("read the group's video, voice and quick-judgement models: %w", err)
	}
	if doc.Entries == nil {
		doc.Entries = []OtohaMediaEntry{}
	}
	return doc.Entries, nil
}

func (s *settingOtohaMediaStore) SaveMediaEntries(ctx context.Context, groupID int64, entries []OtohaMediaEntry) error {
	body, err := json.Marshal(otohaMediaDocument{Entries: entries})
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, otohaMediaEntriesKeyOf(groupID), string(body))
}

// otohaMediaKindRank orders the kinds in the catalog.
func otohaMediaKindRank(kind string) int {
	switch kind {
	case OtohaMediaKindVideo:
		return 0
	case OtohaMediaKindVoice:
		return 1
	default:
		return 2
	}
}

// sortOtohaMediaEntries orders entries by their sort order, then kind, then ID.
func sortOtohaMediaEntries(entries []OtohaMediaEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].SortOrder != entries[j].SortOrder {
			return entries[i].SortOrder < entries[j].SortOrder
		}
		if ri, rj := otohaMediaKindRank(entries[i].Kind), otohaMediaKindRank(entries[j].Kind); ri != rj {
			return ri < rj
		}
		return entries[i].ModelID < entries[j].ModelID
	})
}

// otohaQuickModelIDs are the quick-judgement models the gateway serves: Jev only.
var otohaQuickModelIDs = []string{typesafe.JevLatestModel}

// otohaAllowlistView gives the group's model list for the admin page with the models as a list, never null.
func otohaAllowlistView(list GroupModelAllowlist) GroupModelAllowlist {
	if list.Models == nil {
		list.Models = []string{}
	}
	return list
}
