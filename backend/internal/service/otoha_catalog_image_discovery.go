package service

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/tidwall/gjson"
)

// Image models found from the group's accounts (TASK-76). The group's accounts say what their upstream offers; the
// catalog's image entries are made from that, so the admin does not add them one by one.
//
// What the upstream lists say about a model's kind (checked 2026-10-13):
//   - xAI: GET /v1/models gives an `image_price` on image models; GET /v1/image-generation-models lists exactly the
//     image models with `input_modalities`, `output_modalities` and optional `pricing[]` (quality and resolution).
//   - OpenAI: GET /v1/models gives only id, object, created, owned_by -- no kind. The ChatGPT (Codex) list of an
//     OAuth account never shows image models either.
//   - Compatible gateways (OpenRouter style, models.dev style) give `output_modalities`, `modalities.output`,
//     `architecture.output_modalities`, a `type`, or a `capabilities` field.
//
// So a model counts as an image model when its list says so (OtohaImageEvidenceDeclared). Only when the list gives no
// kind at all, a small table of known image model families decides (otohaKnownImageFamilies,
// OtohaImageEvidenceKnownFamily); for accounts whose list cannot show image models, the gateway's own table of the
// image models it serves does (OtohaImageEvidenceBuiltinCatalog). A model must also be one the gateway's image
// endpoints accept, or the app's request would be refused.

const (
	// OtohaImageSourceManual: the admin added the entry. Discovery never changes it.
	OtohaImageSourceManual = "manual"
	// OtohaImageSourceAuto: discovery made the entry. Discovery refreshes the values the admin did not change.
	OtohaImageSourceAuto = "auto"

	// OtohaCatalogProblemUpstreamMissing: the group's accounts no longer list the model.
	OtohaCatalogProblemUpstreamMissing = "upstream_missing"

	OtohaListedKindImage = "image"
	OtohaListedKindOther = "other"

	OtohaImageEvidenceDeclared       = "declared"
	OtohaImageEvidenceKnownFamily    = "known_family"
	OtohaImageEvidenceBuiltinCatalog = "builtin_catalog"

	OtohaImageResultAdded      = "added"
	OtohaImageResultUpdated    = "updated"
	OtohaImageResultUnchanged  = "unchanged"
	OtohaImageResultManualKept = "manual_kept"
	OtohaImageResultRestored   = "restored"
	OtohaImageResultNotAdded   = "not_added"

	OtohaImageReasonGatewayNotSupported  = "gateway_not_supported"
	OtohaImageReasonBlockedByGroupModels = "blocked_by_group_models"

	OtohaImageListFailed      = "list_failed"
	OtohaImageListUnsupported = "unsupported"

	OtohaImageSkippedGroupPlatform = "group_platform"
	OtohaImageSkippedNoAccounts    = "no_accounts"

	OtohaImageTriggerAdmin    = "admin"
	OtohaImageTriggerSchedule = "schedule"
)

// The fields of an image entry the admin can change; an auto entry remembers which, so a refresh leaves them.
const (
	otohaImageFieldName        = "name"
	otohaImageFieldDescription = "description"
	otohaImageFieldEnabled     = "enabled"
	otohaImageFieldSizes       = "sizes"
	otohaImageFieldQualities   = "qualities"
	otohaImageFieldEdits       = "edits"
	otohaImageFieldPrice1K     = "price_1k"
	otohaImageFieldPrice2K     = "price_2k"
	otohaImageFieldPrice4K     = "price_4k"
)

var ErrOtohaImageDiscoveryUnavailable = infraerrors.New(http.StatusServiceUnavailable, "OTOHA_IMAGE_DISCOVERY_UNAVAILABLE", "image model discovery is not available")

// otohaKnownImageFamilies is the table used only when an upstream list gives no kind. Each prefix is a family the
// gateway's /v1/images endpoints accept (a test holds the two together).
var otohaKnownImageFamilies = []struct{ Platform, Prefix string }{
	{PlatformOpenAI, "gpt-image-"},
	{PlatformGrok, "grok-imagine-image"},
}

func otohaKnownImageFamily(platform, modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	for _, family := range otohaKnownImageFamilies {
		if family.Platform == platform && strings.HasPrefix(id, family.Prefix) {
			return true
		}
	}
	return false
}

// otohaImageDefaults are the sizes, qualities and edit support offered for a model whose list does not say. They are
// the smallest set the provider documents; the admin can widen them.
var otohaImageDefaults = map[string]struct {
	Sizes     []string
	Qualities []string
	Edits     bool
}{
	PlatformOpenAI: {Sizes: []string{"1024x1024", "1536x1024", "1024x1536"}, Qualities: []string{"low", "medium", "high"}, Edits: true},
	PlatformGrok:   {Sizes: []string{"1024x1024"}},
}

// otohaImageResolutionSizes turns the resolutions an xAI list names into sizes the gateway bills in that tier.
var otohaImageResolutionSizes = []struct{ Resolution, Size string }{
	{"1k", "1024x1024"},
	{"1.5k", "1536x1536"},
	{"2k", "2048x2048"},
	{"4k", "4096x4096"},
}

// ---- reading an upstream list ----

// OtohaListedModel is a model an account's upstream lists, with what the list says about it.
type OtohaListedModel struct {
	ID   string
	Name string
	// Kind is OtohaListedKindImage or OtohaListedKindOther when the list says; empty when it gives no kind.
	Kind string
	// DeclaredBy is the field that says Kind is image: output_modalities, capabilities, type, image_price or
	// image_generation_models (the entry is in the upstream's list of image models).
	DeclaredBy  string
	Resolutions []string
	Qualities   []string
	// ImageInput is whether the list says the model takes an image as input; nil when it does not say.
	ImageInput *bool
	// Video is set when the list says the model makes video (TASK-81), VideoDeclaredBy being the field that says so:
	// output_modalities or type.
	Video           bool
	VideoDeclaredBy string
}

var (
	otohaImageKindTypes = map[string]bool{"image": true, "images": true, "image_generation": true, "image-generation": true, "image_model": true}
	otohaOtherKindTypes = map[string]bool{
		"chat": true, "text": true, "language": true, "llm": true, "completion": true, "embedding": true, "embeddings": true,
		"audio": true, "tts": true, "stt": true, "moderation": true, "video": true, "rerank": true,
	}
	otohaImageCapabilities = map[string]bool{"image_generation": true, "image-generation": true, "generate_images": true, "images": true}
)

// parseOtohaListedModels reads a model list body (`data`, `models` or a bare array). imageList says the list is the
// upstream's list of image models, so every entry in it is one.
func parseOtohaListedModels(body []byte, imageList bool) []OtohaListedModel {
	root := gjson.ParseBytes(body)
	entries := root
	if root.IsObject() {
		entries = root.Get("data")
		if !entries.IsArray() {
			entries = root.Get("models")
		}
	}
	out := []OtohaListedModel{}
	seen := map[string]bool{}
	entries.ForEach(func(_, entry gjson.Result) bool {
		model, ok := parseOtohaListedEntry(entry, imageList)
		if ok && !seen[model.ID] {
			seen[model.ID] = true
			out = append(out, model)
		}
		return true
	})
	return out
}

func parseOtohaListedEntry(entry gjson.Result, imageList bool) (OtohaListedModel, bool) {
	if !entry.IsObject() {
		return OtohaListedModel{}, false
	}
	id := ""
	for _, key := range []string{"id", "slug", "model", "model_id", "modelId"} {
		if v := strings.TrimSpace(entry.Get(key).String()); v != "" {
			id = v
			break
		}
	}
	id = strings.TrimPrefix(id, "models/")
	if id == "" {
		return OtohaListedModel{}, false
	}
	model := OtohaListedModel{ID: id}
	if name := strings.TrimSpace(entry.Get("display_name").String()); name != "" {
		model.Name = name
	} else if name := strings.TrimSpace(entry.Get("name").String()); name != "" && name != id {
		model.Name = name
	}

	outputs := otohaStringList(entry, "output_modalities", "modalities.output", "architecture.output_modalities")
	if otohaContains(outputs, "video") {
		model.Video, model.VideoDeclaredBy = true, "output_modalities"
	} else if kind := strings.ToLower(strings.TrimSpace(entry.Get("type").String())); kind == "video" {
		model.Video, model.VideoDeclaredBy = true, "type"
	}
	switch {
	case len(outputs) > 0:
		if otohaContains(outputs, "image") {
			model.Kind, model.DeclaredBy = OtohaListedKindImage, "output_modalities"
		} else {
			model.Kind = OtohaListedKindOther
		}
	case otohaCapabilityImage(entry.Get("capabilities")):
		model.Kind, model.DeclaredBy = OtohaListedKindImage, "capabilities"
	default:
		kind := strings.ToLower(strings.TrimSpace(entry.Get("type").String()))
		if kind == "" {
			kind = strings.ToLower(strings.TrimSpace(entry.Get("kind").String()))
		}
		switch {
		case otohaImageKindTypes[kind]:
			model.Kind, model.DeclaredBy = OtohaListedKindImage, "type"
		case otohaOtherKindTypes[kind]:
			model.Kind = OtohaListedKindOther
		case entry.Get("image_price").Exists() && entry.Get("image_price").Type != gjson.Null:
			model.Kind, model.DeclaredBy = OtohaListedKindImage, "image_price"
		}
	}
	if imageList && model.Kind != OtohaListedKindOther {
		model.Kind, model.DeclaredBy = OtohaListedKindImage, "image_generation_models"
	}

	entry.Get("pricing").ForEach(func(_, price gjson.Result) bool {
		if resolution := strings.ToLower(strings.TrimSpace(price.Get("resolution").String())); resolution != "" && !otohaContains(model.Resolutions, resolution) {
			model.Resolutions = append(model.Resolutions, resolution)
		}
		if quality := strings.ToLower(strings.TrimSpace(price.Get("quality").String())); quality != "" && !otohaContains(model.Qualities, quality) {
			model.Qualities = append(model.Qualities, quality)
		}
		return true
	})
	if inputs := otohaStringList(entry, "input_modalities", "modalities.input", "architecture.input_modalities"); len(inputs) > 0 {
		image := otohaContains(inputs, "image")
		model.ImageInput = &image
	}
	return model, true
}

func otohaStringList(entry gjson.Result, paths ...string) []string {
	for _, path := range paths {
		value := entry.Get(path)
		if !value.IsArray() {
			continue
		}
		out := []string{}
		for _, item := range value.Array() {
			if s := strings.ToLower(strings.TrimSpace(item.String())); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func otohaCapabilityImage(capabilities gjson.Result) bool {
	switch {
	case capabilities.IsObject():
		found := false
		capabilities.ForEach(func(key, value gjson.Result) bool {
			if otohaImageCapabilities[strings.ToLower(key.String())] && value.Bool() {
				found = true
			}
			return !found
		})
		return found
	case capabilities.IsArray():
		for _, item := range capabilities.Array() {
			if otohaImageCapabilities[strings.ToLower(strings.TrimSpace(item.String()))] {
				return true
			}
		}
	}
	return false
}

// ---- candidates ----

// OtohaAccountListing is what one account of the group lists. Err is OtohaImageListFailed or
// OtohaImageListUnsupported when the list could not be read.
type OtohaAccountListing struct {
	Account *Account
	Models  []OtohaListedModel
	Err     string
}

// otohaImageCandidate is an image model some account of the group offers.
type otohaImageCandidate struct {
	ModelID    string
	Name       string
	Platform   string
	Accounts   []string
	AccountIDs []int64
	// Served is how many of those accounts serve the model (their model mapping lets it through).
	Served      int
	Evidence    string
	DeclaredBy  string
	Resolutions []string
	Qualities   []string
	Edits       *bool
}

func otohaEvidenceRank(evidence string) int {
	switch evidence {
	case OtohaImageEvidenceDeclared:
		return 3
	case OtohaImageEvidenceKnownFamily:
		return 2
	default:
		return 1
	}
}

// otohaImageCandidatesFrom finds the image models the listings offer, one candidate per model ID, by ID.
func otohaImageCandidatesFrom(listings []OtohaAccountListing) []otohaImageCandidate {
	byID := map[string]*otohaImageCandidate{}
	add := func(account *Account, id, name, evidence, declaredBy string, listed *OtohaListedModel, served bool) {
		c, ok := byID[id]
		if !ok {
			c = &otohaImageCandidate{ModelID: id, Platform: account.Platform, Evidence: evidence, DeclaredBy: declaredBy}
			byID[id] = c
		}
		if !otohaContains(c.Accounts, account.Name) {
			c.Accounts = append(c.Accounts, account.Name)
		}
		if !containsOtohaID(c.AccountIDs, account.ID) {
			c.AccountIDs = append(c.AccountIDs, account.ID)
		}
		if served {
			c.Served++
		}
		if c.Name == "" {
			c.Name = name
		}
		if otohaEvidenceRank(evidence) > otohaEvidenceRank(c.Evidence) {
			c.Evidence, c.DeclaredBy = evidence, declaredBy
		}
		if listed != nil {
			if len(c.Resolutions) == 0 {
				c.Resolutions = listed.Resolutions
			}
			if len(c.Qualities) == 0 {
				c.Qualities = listed.Qualities
			}
			if c.Edits == nil && listed.ImageInput != nil {
				edits := *listed.ImageInput
				c.Edits = &edits
			}
		}
	}

	for _, listing := range listings {
		account := listing.Account
		if account == nil {
			continue
		}
		listedByID := make(map[string]OtohaListedModel, len(listing.Models))
		for _, m := range listing.Models {
			listedByID[m.ID] = m
		}
		// imageEvidence says whether the account's upstream offers the model as an image model, and on what grounds.
		imageEvidence := func(id string) (string, string, bool) {
			m, ok := listedByID[id]
			if !ok || m.Kind == OtohaListedKindOther {
				return "", "", false
			}
			if m.Kind == OtohaListedKindImage {
				return OtohaImageEvidenceDeclared, m.DeclaredBy, true
			}
			if otohaKnownImageFamily(account.Platform, id) {
				return OtohaImageEvidenceKnownFamily, "", true
			}
			return "", "", false
		}
		// The gateway's own table, for accounts whose list cannot show image models.
		builtin := map[string]string{}
		switch {
		case account.IsOpenAIOAuthLike():
			for _, model := range openai.DefaultModels {
				if !otohaKnownImageFamily(PlatformOpenAI, model.ID) || !account.IsModelSupported(model.ID) {
					continue
				}
				if !account.IsOpenAIPassthroughEnabled() && !IsGPTImageGenerationModel(account.GetMappedModel(model.ID)) {
					continue
				}
				builtin[model.ID] = model.DisplayName
			}
		case account.IsGrok() && listing.Err != "":
			for _, model := range xai.DefaultModels() {
				if otohaKnownImageFamily(PlatformGrok, model.ID) && account.IsModelSupported(model.ID) {
					builtin[model.ID] = model.DisplayName
				}
			}
		}

		handled := map[string]bool{}
		for _, m := range listing.Models {
			evidence, declaredBy, ok := imageEvidence(m.ID)
			if !ok {
				continue
			}
			handled[m.ID] = true
			listed := m
			add(account, m.ID, m.Name, evidence, declaredBy, &listed, account.IsModelSupported(m.ID))
		}
		for id, name := range builtin {
			if handled[id] {
				continue
			}
			handled[id] = true
			add(account, id, name, OtohaImageEvidenceBuiltinCatalog, "", nil, true)
		}
		// An alias the admin set in the account's model mapping that points at an image model (the gateway's own
		// default aliases are not read: they are spellings of models the list already names).
		for alias, target := range explicitOtohaModelMapping(account) {
			if strings.Contains(alias, "*") || handled[alias] || alias == target {
				continue
			}
			evidence, declaredBy, ok := imageEvidence(target)
			var name string
			var listed *OtohaListedModel
			if m, found := listedByID[target]; found && ok {
				name, listed = m.Name, &m
			}
			if !ok {
				if display, isBuiltin := builtin[target]; isBuiltin {
					evidence, declaredBy, ok, name = OtohaImageEvidenceBuiltinCatalog, "", true, display
				}
			}
			if ok {
				handled[alias] = true
				add(account, alias, name, evidence, declaredBy, listed, true)
			}
		}
	}

	out := make([]otohaImageCandidate, 0, len(byID))
	for _, c := range byID {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelID < out[j].ModelID })
	return out
}

// ---- the report ----

// OtohaImageDiscovery is what the last check of a group's accounts found, for the admin page.
type OtohaImageDiscovery struct {
	RanAt         *time.Time                     `json:"ran_at"`
	Trigger       string                         `json:"trigger"`
	SkippedReason string                         `json:"skipped_reason"`
	Accounts      []OtohaImageDiscoveryAccount   `json:"accounts"`
	Candidates    []OtohaImageDiscoveryCandidate `json:"candidates"`
	// Missing are the auto entries whose model the group's accounts no longer list.
	Missing []string `json:"missing"`
}

// OtohaImageDiscoveryAccount is an account that was asked, how many models it listed and how many of them are image
// models; Error is set when its list could not be read.
type OtohaImageDiscoveryAccount struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Platform    string `json:"platform"`
	Type        string `json:"type"`
	Listed      int    `json:"listed"`
	ImageModels int    `json:"image_models"`
	Error       string `json:"error"`
}

// OtohaImageDiscoveryCandidate is an image model found, what became of it and, when it was not added, why.
type OtohaImageDiscoveryCandidate struct {
	ModelID    string   `json:"model_id"`
	Name       string   `json:"name"`
	Accounts   []string `json:"accounts"`
	Evidence   string   `json:"evidence"`
	DeclaredBy string   `json:"declared_by"`
	Result     string   `json:"result"`
	Reason     string   `json:"reason"`
}

// ---- the plan ----

type otohaImagePlanInput struct {
	Candidates []otohaImageCandidate
	Existing   []OtohaImageEntry
	// AllListed is whether every account's list was read; only then can a model be called gone.
	AllListed bool
	// Reason says why a candidate cannot be added ("" when it can).
	Reason func(otohaImageCandidate) string
	Now    time.Time
}

type otohaImagePlan struct {
	Create     []OtohaImageEntry
	Update     []OtohaImageEntry
	Candidates []OtohaImageDiscoveryCandidate
	Missing    []string
}

type otohaDerivedImageFields struct {
	name      string
	sizes     []string
	qualities []string
	edits     bool
}

// deriveOtohaImageFields gives the values an auto entry has from what the upstream lists said, else the provider's
// documented defaults.
func deriveOtohaImageFields(c otohaImageCandidate) otohaDerivedImageFields {
	defaults := otohaImageDefaults[c.Platform]
	derived := otohaDerivedImageFields{name: c.Name, sizes: defaults.Sizes, qualities: defaults.Qualities, edits: defaults.Edits}
	if derived.name == "" {
		derived.name = c.ModelID
	}
	if len(c.Resolutions) > 0 {
		sizes := []string{}
		for _, known := range otohaImageResolutionSizes {
			if otohaContains(c.Resolutions, known.Resolution) {
				sizes = append(sizes, known.Size)
			}
		}
		if len(sizes) > 0 {
			derived.sizes = sizes
		}
	}
	if len(c.Qualities) > 0 {
		qualities := []string{}
		for _, q := range c.Qualities {
			if otohaReasoningPattern.MatchString(q) && !otohaContains(qualities, q) {
				qualities = append(qualities, q)
			}
		}
		derived.qualities = qualities
	}
	if c.Edits != nil {
		derived.edits = *c.Edits
	}
	derived.sizes = append([]string{}, derived.sizes...)
	derived.qualities = append([]string{}, derived.qualities...)
	return derived
}

func sameOtohaStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// planOtohaImageDiscovery decides what the check does to the group's image entries: which to make, which to refresh
// (never a value the admin changed), which to mark gone, and what to tell the admin.
func planOtohaImageDiscovery(in otohaImagePlanInput) otohaImagePlan {
	plan := otohaImagePlan{Candidates: []OtohaImageDiscoveryCandidate{}, Missing: []string{}}
	existing := make(map[string]OtohaImageEntry, len(in.Existing))
	nextOrder := 0
	for _, e := range in.Existing {
		existing[e.ModelID] = e
		if e.SortOrder > nextOrder {
			nextOrder = e.SortOrder
		}
	}
	found := map[string]bool{}
	for _, c := range in.Candidates {
		found[c.ModelID] = true
		report := OtohaImageDiscoveryCandidate{ModelID: c.ModelID, Name: c.Name, Accounts: append([]string{}, c.Accounts...), Evidence: c.Evidence, DeclaredBy: c.DeclaredBy}
		if report.Name == "" {
			report.Name = c.ModelID
		}
		if in.Reason != nil {
			report.Reason = in.Reason(c)
		}
		if report.Reason == "" && c.Served == 0 {
			report.Reason = OtohaCatalogProblemNoAccount
		}
		current, has := existing[c.ModelID]
		derived := deriveOtohaImageFields(c)
		switch {
		case !has && report.Reason != "":
			report.Result = OtohaImageResultNotAdded
		case !has:
			nextOrder += otohaSortOrderStep
			plan.Create = append(plan.Create, OtohaImageEntry{
				ModelID: c.ModelID, Name: derived.name, Enabled: true, SortOrder: nextOrder,
				Sizes: derived.sizes, Qualities: derived.qualities, Edits: derived.edits,
				Source: OtohaImageSourceAuto, ManualFields: []string{},
			})
			report.Result = OtohaImageResultAdded
		case current.Source != OtohaImageSourceAuto:
			report.Result = OtohaImageResultManualKept
		default:
			refreshed, changed := refreshOtohaAutoImageEntry(current, derived)
			restored := refreshed.UnavailableSince != nil
			refreshed.UnavailableSince = nil
			switch {
			case restored:
				report.Result = OtohaImageResultRestored
				plan.Update = append(plan.Update, refreshed)
			case changed:
				report.Result = OtohaImageResultUpdated
				plan.Update = append(plan.Update, refreshed)
			default:
				report.Result = OtohaImageResultUnchanged
			}
		}
		plan.Candidates = append(plan.Candidates, report)
	}

	for _, e := range in.Existing {
		if e.Source != OtohaImageSourceAuto || found[e.ModelID] || !in.AllListed {
			continue
		}
		plan.Missing = append(plan.Missing, e.ModelID)
		if e.UnavailableSince == nil {
			since := in.Now
			e.UnavailableSince = &since
			plan.Update = append(plan.Update, e)
		}
	}
	sort.Strings(plan.Missing)
	return plan
}

// refreshOtohaAutoImageEntry sets the derived values on an auto entry except those the admin changed. The enabled
// switch is never refreshed. changed says whether a value differs.
func refreshOtohaAutoImageEntry(current OtohaImageEntry, derived otohaDerivedImageFields) (OtohaImageEntry, bool) {
	manual := func(field string) bool { return otohaContains(current.ManualFields, field) }
	out := current
	changed := false
	if !manual(otohaImageFieldName) && out.Name != derived.name {
		out.Name, changed = derived.name, true
	}
	if !manual(otohaImageFieldSizes) && !sameOtohaStrings(out.Sizes, derived.sizes) {
		out.Sizes, changed = derived.sizes, true
	}
	if !manual(otohaImageFieldQualities) && !sameOtohaStrings(out.Qualities, derived.qualities) {
		out.Qualities, changed = derived.qualities, true
	}
	if !manual(otohaImageFieldEdits) && out.Edits != derived.edits {
		out.Edits, changed = derived.edits, true
	}
	return out, changed
}

// explicitOtohaModelMapping is the model mapping the admin wrote on the account, without the platform defaults.
func explicitOtohaModelMapping(account *Account) map[string]string {
	out := map[string]string{}
	switch raw := account.Credentials["model_mapping"].(type) {
	case map[string]any:
		for alias, target := range raw {
			if text, ok := target.(string); ok {
				out[alias] = strings.TrimSpace(text)
			}
		}
	case map[string]string:
		for alias, target := range raw {
			out[alias] = strings.TrimSpace(target)
		}
	}
	return out
}

func containsOtohaID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func equalOtohaPrice(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// changedOtohaImageFields names the fields an admin's edit changed.
func changedOtohaImageFields(before, after OtohaImageEntry) []string {
	changed := []string{}
	if before.Name != after.Name {
		changed = append(changed, otohaImageFieldName)
	}
	if before.Description != after.Description {
		changed = append(changed, otohaImageFieldDescription)
	}
	if before.Enabled != after.Enabled {
		changed = append(changed, otohaImageFieldEnabled)
	}
	if !sameOtohaStrings(before.Sizes, after.Sizes) {
		changed = append(changed, otohaImageFieldSizes)
	}
	if !sameOtohaStrings(before.Qualities, after.Qualities) {
		changed = append(changed, otohaImageFieldQualities)
	}
	if before.Edits != after.Edits {
		changed = append(changed, otohaImageFieldEdits)
	}
	if !equalOtohaPrice(before.Price1K, after.Price1K) {
		changed = append(changed, otohaImageFieldPrice1K)
	}
	if !equalOtohaPrice(before.Price2K, after.Price2K) {
		changed = append(changed, otohaImageFieldPrice2K)
	}
	if !equalOtohaPrice(before.Price4K, after.Price4K) {
		changed = append(changed, otohaImageFieldPrice4K)
	}
	return changed
}

// ---- the service ----

// OtohaImageListingSource reads what each active OpenAI and Grok account of a group lists.
type OtohaImageListingSource interface {
	ListGroupAccountModels(ctx context.Context, groupID int64) ([]OtohaAccountListing, error)
}

// OtohaImageDiscoveryStore keeps the last check's report of a group.
type OtohaImageDiscoveryStore interface {
	LoadImageDiscovery(ctx context.Context, groupID int64) (*OtohaImageDiscovery, error)
	SaveImageDiscovery(ctx context.Context, groupID int64, report OtohaImageDiscovery) error
}

type otohaImageDiscoveryState struct {
	listings OtohaImageListingSource
	store    OtohaImageDiscoveryStore
	mu       sync.Mutex
}

// WithDiscovery gives the service the means to make image entries from the group's accounts.
func (s *OtohaCatalogService) WithDiscovery(listings OtohaImageListingSource, store OtohaImageDiscoveryStore) *OtohaCatalogService {
	s.discovery = &otohaImageDiscoveryState{listings: listings, store: store}
	return s
}

func (s *OtohaCatalogService) lastImageDiscovery(ctx context.Context, groupID int64) *OtohaImageDiscovery {
	if s.discovery == nil || s.discovery.store == nil {
		return nil
	}
	report, err := s.discovery.store.LoadImageDiscovery(ctx, groupID)
	if err != nil {
		return nil
	}
	return report
}

// DiscoverImages asks the group's accounts what they offer, makes the group's image entries from the image models
// among it, refreshes the ones it made (leaving what the admin changed), marks those the upstream no longer lists,
// and keeps a report of what it found and why something was not added. It never changes the group's own switch for
// image generation.
func (s *OtohaCatalogService) DiscoverImages(ctx context.Context, groupID int64, trigger string) (*OtohaImageDiscovery, error) {
	if s == nil || s.discovery == nil || s.discovery.listings == nil || s.images == nil {
		return nil, ErrOtohaImageDiscoveryUnavailable
	}
	s.discovery.mu.Lock()
	defer s.discovery.mu.Unlock()

	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	report := &OtohaImageDiscovery{
		RanAt: &now, Trigger: trigger,
		Accounts: []OtohaImageDiscoveryAccount{}, Candidates: []OtohaImageDiscoveryCandidate{}, Missing: []string{},
	}
	save := func() (*OtohaImageDiscovery, error) {
		if s.discovery.store != nil {
			if err := s.discovery.store.SaveImageDiscovery(ctx, groupID, *report); err != nil {
				return nil, err
			}
		}
		s.InvalidateGroup(groupID)
		return report, nil
	}

	switch group.Platform {
	case PlatformOpenAI, PlatformGrok, PlatformComposite:
	default:
		report.SkippedReason = OtohaImageSkippedGroupPlatform
		return save()
	}
	listings, err := s.discovery.listings.ListGroupAccountModels(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if len(listings) == 0 {
		report.SkippedReason = OtohaImageSkippedNoAccounts
		return save()
	}

	candidates := otohaImageCandidatesFrom(listings)
	existing, err := s.listImages(ctx, groupID)
	if err != nil {
		return nil, err
	}
	allListed := true
	for _, listing := range listings {
		if listing.Err != "" {
			allListed = false
		}
	}
	plan := planOtohaImageDiscovery(otohaImagePlanInput{
		Candidates: candidates, Existing: existing, AllListed: allListed, Now: now,
		Reason: func(c otohaImageCandidate) string {
			if !isOpenAIImageGenerationModel(c.ModelID) {
				return OtohaImageReasonGatewayNotSupported
			}
			if group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(c.ModelID) {
				return OtohaImageReasonBlockedByGroupModels
			}
			if s.imageRouting != nil {
				if route := s.imageRouting.ImageRoute(ctx, group, c.ModelID); route.Problem != "" {
					return route.Problem
				}
			}
			return ""
		},
	})
	for _, entry := range plan.Create {
		entry.GroupID = groupID
		if err := s.images.CreateImage(ctx, &entry); err != nil && !errors.Is(err, ErrOtohaCatalogEntryExists) {
			return nil, err
		}
	}
	for _, entry := range plan.Update {
		if err := s.images.UpdateImage(ctx, &entry); err != nil && !errors.Is(err, ErrOtohaCatalogEntryNotFound) {
			return nil, err
		}
	}

	imageCount := map[int64]int{}
	for _, c := range candidates {
		for _, id := range c.AccountIDs {
			imageCount[id]++
		}
	}
	for _, listing := range listings {
		if listing.Account == nil {
			continue
		}
		report.Accounts = append(report.Accounts, OtohaImageDiscoveryAccount{
			ID: listing.Account.ID, Name: listing.Account.Name, Platform: listing.Account.Platform, Type: listing.Account.Type,
			Listed: len(listing.Models), ImageModels: imageCount[listing.Account.ID], Error: listing.Err,
		})
	}
	report.Candidates = plan.Candidates
	report.Missing = plan.Missing
	return save()
}
