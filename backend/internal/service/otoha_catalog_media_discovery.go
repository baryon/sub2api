package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

// Video, voice and quick-judgement models found from the group's accounts (TASK-81), the way image models are
// (TASK-76): the accounts say what their upstream offers, and the catalog's entries are made from that.
//
//   - Video: a Grok account's list names the video models. A list that says so (output_modalities, type) declares one
//     (OtohaImageEvidenceDeclared); one that gives no kind (xAI's /v1/models) is read with the one known family
//     (OtohaImageEvidenceKnownFamily, "grok-imagine-video", which billing and the video endpoint also know); an
//     account whose list could not be read gets the gateway's own table of xAI models (OtohaImageEvidenceBuiltinCatalog).
//   - Voice: xAI lists no voice models. A Grok account offers the two functions the gateway forwards and bills,
//     text to speech and speech to text (OtohaImageEvidenceBuiltinCatalog).
//   - Quick judgement: a TypeSafe account has no model list; the gateway accepts exactly typesafe.JevLatestModel
//     (OtohaImageEvidenceBuiltinCatalog).
//   - Gemini video (Veo): a Gemini list names the models that take predictLongRunning (OtohaImageEvidenceDeclared).
//     The gateway forwards no such call and billing has no price for a second of Veo, so they are reported, never
//     added (TASK-85).

const (
	otohaGrokVideoFamily = "grok-imagine-video"

	otohaVoiceNameTTS = "Grok text to speech"
	otohaVoiceNameSTT = "Grok speech to text"
	otohaQuickNameJev = "Jev"
)

func otohaKnownVideoFamily(modelID string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelID)), otohaGrokVideoFamily)
}

// otohaMediaCandidate is a video, voice or quick-judgement model some account of the group offers.
type otohaMediaCandidate struct {
	Kind       string
	ModelID    string
	Name       string
	Platform   string
	Accounts   []string
	AccountIDs []int64
	// Served is how many of those accounts serve the model.
	Served     int
	Evidence   string
	DeclaredBy string
}

// OtohaMediaAccountSource reads what each active Grok and TypeSafe account of a group offers.
type OtohaMediaAccountSource interface {
	ListGroupMediaAccounts(ctx context.Context, groupID int64) ([]OtohaAccountListing, error)
}

func otohaMediaCandidatesFrom(listings []OtohaAccountListing) []otohaMediaCandidate {
	byKey := map[string]*otohaMediaCandidate{}
	add := func(account *Account, kind, id, name, evidence, declaredBy string, served bool) {
		key := kind + "/" + id
		c, ok := byKey[key]
		if !ok {
			c = &otohaMediaCandidate{Kind: kind, ModelID: id, Platform: account.Platform, Evidence: evidence, DeclaredBy: declaredBy}
			byKey[key] = c
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
	}
	gatewayVideoNames := map[string]string{}
	for _, model := range xai.DefaultModels() {
		if otohaKnownVideoFamily(model.ID) {
			gatewayVideoNames[model.ID] = model.DisplayName
		}
	}

	for _, listing := range listings {
		account := listing.Account
		if account == nil {
			continue
		}
		switch {
		case account.IsGrok():
			handled := map[string]bool{}
			for _, m := range listing.Models {
				evidence, declaredBy := "", ""
				switch {
				case m.Video:
					evidence, declaredBy = OtohaImageEvidenceDeclared, m.VideoDeclaredBy
				case m.Kind == "" && otohaKnownVideoFamily(m.ID):
					evidence = OtohaImageEvidenceKnownFamily
				default:
					continue
				}
				name := m.Name
				if name == "" {
					name = gatewayVideoNames[m.ID]
				}
				handled[m.ID] = true
				add(account, OtohaMediaKindVideo, m.ID, name, evidence, declaredBy, account.IsModelSupported(m.ID))
			}
			if listing.Err != "" {
				for id, name := range gatewayVideoNames {
					if !handled[id] && account.IsModelSupported(id) {
						add(account, OtohaMediaKindVideo, id, name, OtohaImageEvidenceBuiltinCatalog, "", true)
					}
				}
			}
			add(account, OtohaMediaKindVoice, OtohaVoiceModeTTS, otohaVoiceNameTTS, OtohaImageEvidenceBuiltinCatalog, "", true)
			add(account, OtohaMediaKindVoice, OtohaVoiceModeSTT, otohaVoiceNameSTT, OtohaImageEvidenceBuiltinCatalog, "", true)
		case account.IsGemini():
			for _, m := range listing.Models {
				if m.Video {
					add(account, OtohaMediaKindVideo, m.ID, m.Name, OtohaImageEvidenceDeclared, m.VideoDeclaredBy, account.IsModelSupported(m.ID))
				}
			}
		case account.IsTypeSafe():
			for _, id := range otohaQuickModelIDs {
				add(account, OtohaMediaKindQuick, id, otohaQuickNameJev, OtohaImageEvidenceBuiltinCatalog, "", true)
			}
		}
	}

	out := make([]otohaMediaCandidate, 0, len(byKey))
	for _, c := range byKey {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := otohaMediaKindRank(out[i].Kind), otohaMediaKindRank(out[j].Kind); ri != rj {
			return ri < rj
		}
		if out[i].Kind == OtohaMediaKindVoice {
			// Text to speech before speech to text, as the functions are usually named.
			return out[i].ModelID > out[j].ModelID
		}
		return out[i].ModelID < out[j].ModelID
	})
	return out
}

// ---- the report ----

// OtohaMediaDiscovery is what the last check of a group's accounts found for these models, for the admin page.
type OtohaMediaDiscovery struct {
	RanAt         *time.Time                     `json:"ran_at"`
	Trigger       string                         `json:"trigger"`
	SkippedReason string                         `json:"skipped_reason"`
	Accounts      []OtohaMediaDiscoveryAccount   `json:"accounts"`
	Candidates    []OtohaMediaDiscoveryCandidate `json:"candidates"`
	// Missing are the entries ("kind/model") whose model the group's accounts no longer offer.
	Missing []string `json:"missing"`
}

// OtohaMediaDiscoveryAccount is an account that was asked and how many of these models it offers; Error is set when
// its list could not be read.
type OtohaMediaDiscoveryAccount struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Platform    string `json:"platform"`
	Type        string `json:"type"`
	Listed      int    `json:"listed"`
	MediaModels int    `json:"media_models"`
	Error       string `json:"error"`
}

// OtohaMediaDiscoveryCandidate is a model found, what became of it and, when it was not added, why.
type OtohaMediaDiscoveryCandidate struct {
	Kind       string   `json:"kind"`
	ModelID    string   `json:"model_id"`
	Name       string   `json:"name"`
	Accounts   []string `json:"accounts"`
	Evidence   string   `json:"evidence"`
	DeclaredBy string   `json:"declared_by"`
	Result     string   `json:"result"`
	Reason     string   `json:"reason"`
}

// OtohaMediaDiscoveryStore keeps the last check's report of a group.
type OtohaMediaDiscoveryStore interface {
	LoadMediaDiscovery(ctx context.Context, groupID int64) (*OtohaMediaDiscovery, error)
	SaveMediaDiscovery(ctx context.Context, groupID int64, report OtohaMediaDiscovery) error
}

// ---- the plan ----

type otohaMediaPlanInput struct {
	Candidates []otohaMediaCandidate
	Existing   []OtohaMediaEntry
	// AllListed is whether every account's list was read; only then can a model be called gone.
	AllListed bool
	// Reason says why a candidate cannot be added ("" when it can).
	Reason func(otohaMediaCandidate) string
	Now    time.Time
}

type otohaMediaPlan struct {
	// Entries are all the group's entries after the check, in order.
	Entries    []OtohaMediaEntry
	Changed    bool
	Candidates []OtohaMediaDiscoveryCandidate
	Missing    []string
}

func otohaMediaKey(kind, modelID string) string { return kind + "/" + modelID }

// planOtohaMediaDiscovery decides what the check does to the group's entries: which to make, which to refresh (never
// a value the admin changed), which to mark gone, and what to tell the admin.
func planOtohaMediaDiscovery(in otohaMediaPlanInput) otohaMediaPlan {
	plan := otohaMediaPlan{Candidates: []OtohaMediaDiscoveryCandidate{}, Missing: []string{}}
	plan.Entries = append([]OtohaMediaEntry{}, in.Existing...)
	index := make(map[string]int, len(plan.Entries))
	nextOrder := 0
	for i, e := range plan.Entries {
		index[otohaMediaKey(e.Kind, e.ModelID)] = i
		if e.SortOrder > nextOrder {
			nextOrder = e.SortOrder
		}
	}
	found := map[string]bool{}
	for _, c := range in.Candidates {
		key := otohaMediaKey(c.Kind, c.ModelID)
		found[key] = true
		report := OtohaMediaDiscoveryCandidate{
			Kind: c.Kind, ModelID: c.ModelID, Name: c.Name, Accounts: append([]string{}, c.Accounts...), Evidence: c.Evidence, DeclaredBy: c.DeclaredBy,
		}
		if report.Name == "" {
			report.Name = c.ModelID
		}
		if in.Reason != nil {
			report.Reason = in.Reason(c)
		}
		if report.Reason == "" && c.Served == 0 {
			report.Reason = OtohaCatalogProblemNoAccount
		}
		at, has := index[key]
		switch {
		case !has && report.Reason != "":
			report.Result = OtohaImageResultNotAdded
		case !has:
			nextOrder += otohaSortOrderStep
			index[key] = len(plan.Entries)
			plan.Entries = append(plan.Entries, OtohaMediaEntry{
				Kind: c.Kind, ModelID: c.ModelID, Name: report.Name, Enabled: true, SortOrder: nextOrder, ManualFields: []string{},
			})
			plan.Changed = true
			report.Result = OtohaImageResultAdded
		default:
			current := plan.Entries[at]
			restored := current.UnavailableSince != nil
			if restored {
				current.UnavailableSince = nil
			}
			changed := false
			// The name is the only value discovery derives; the switch and the description are never refreshed.
			if !otohaContains(current.ManualFields, otohaMediaFieldName) && current.Name != report.Name {
				current.Name, changed = report.Name, true
			}
			switch {
			case restored:
				report.Result = OtohaImageResultRestored
			case changed:
				report.Result = OtohaImageResultUpdated
			default:
				report.Result = OtohaImageResultUnchanged
			}
			if restored || changed {
				plan.Entries[at] = current
				plan.Changed = true
			}
		}
		plan.Candidates = append(plan.Candidates, report)
	}

	if in.AllListed {
		for i, e := range plan.Entries {
			key := otohaMediaKey(e.Kind, e.ModelID)
			if found[key] {
				continue
			}
			plan.Missing = append(plan.Missing, key)
			if e.UnavailableSince == nil {
				since := in.Now
				plan.Entries[i].UnavailableSince = &since
				plan.Changed = true
			}
		}
	}
	sort.Strings(plan.Missing)
	return plan
}

// ---- the service ----

// WithMediaDiscovery gives the service the means to make media entries from the group's accounts.
func (s *OtohaCatalogService) WithMediaDiscovery(accounts OtohaMediaAccountSource, reports OtohaMediaDiscoveryStore) *OtohaCatalogService {
	if s.media == nil {
		s.media = &otohaMediaState{}
	}
	s.media.accounts, s.media.reports = accounts, reports
	return s
}

func (s *OtohaCatalogService) lastMediaDiscovery(ctx context.Context, groupID int64) *OtohaMediaDiscovery {
	if s.media == nil || s.media.reports == nil {
		return nil
	}
	report, err := s.media.reports.LoadMediaDiscovery(ctx, groupID)
	if err != nil {
		return nil
	}
	return report
}

// DiscoverMedia asks the group's accounts what video, voice and quick-judgement models they offer, makes the group's
// entries from it, refreshes what it made (leaving what the admin changed), marks those the accounts no longer offer,
// and keeps a report of what it found and why something was not added.
func (s *OtohaCatalogService) DiscoverMedia(ctx context.Context, groupID int64, trigger string) (*OtohaMediaDiscovery, error) {
	if s == nil || s.media == nil || s.media.accounts == nil || s.media.store == nil {
		return nil, ErrOtohaMediaDiscoveryUnavailable
	}
	s.media.mu.Lock()
	defer s.media.mu.Unlock()

	group, err := s.groups.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	report := &OtohaMediaDiscovery{
		RanAt: &now, Trigger: trigger,
		Accounts: []OtohaMediaDiscoveryAccount{}, Candidates: []OtohaMediaDiscoveryCandidate{}, Missing: []string{},
	}
	save := func() (*OtohaMediaDiscovery, error) {
		if s.media.reports != nil {
			if err := s.media.reports.SaveMediaDiscovery(ctx, groupID, *report); err != nil {
				return nil, err
			}
		}
		s.InvalidateGroup(groupID)
		return report, nil
	}

	switch group.Platform {
	case PlatformGrok, PlatformTypeSafe, PlatformGemini, PlatformComposite:
	default:
		report.SkippedReason = OtohaImageSkippedGroupPlatform
		return save()
	}
	listings, err := s.media.accounts.ListGroupMediaAccounts(ctx, groupID)
	if err != nil {
		return nil, err
	}
	candidates := otohaMediaCandidatesFrom(listings)
	existing, err := s.media.store.LoadMediaEntries(ctx, groupID)
	if err != nil {
		return nil, err
	}
	allListed := true
	for _, listing := range listings {
		if listing.Err != "" {
			allListed = false
		}
	}
	plan := planOtohaMediaDiscovery(otohaMediaPlanInput{
		Candidates: candidates, Existing: existing, AllListed: allListed, Now: now,
		Reason: func(c otohaMediaCandidate) string { return s.mediaCandidateReason(ctx, group, c) },
	})
	if plan.Changed {
		sortOtohaMediaEntries(plan.Entries)
		if err := s.media.store.SaveMediaEntries(ctx, groupID, plan.Entries); err != nil {
			return nil, err
		}
	}

	count := map[int64]int{}
	for _, c := range candidates {
		for _, id := range c.AccountIDs {
			count[id]++
		}
	}
	for _, listing := range listings {
		if listing.Account == nil {
			continue
		}
		report.Accounts = append(report.Accounts, OtohaMediaDiscoveryAccount{
			ID: listing.Account.ID, Name: listing.Account.Name, Platform: listing.Account.Platform, Type: listing.Account.Type,
			Listed: len(listing.Models), MediaModels: count[listing.Account.ID], Error: listing.Err,
		})
	}
	report.Candidates = plan.Candidates
	report.Missing = plan.Missing
	return save()
}

// mediaCandidateReason says why a candidate cannot be made an entry: the gateway does not serve the model, the
// group's model list blocks it (a voice request names no model, so the list does not apply to it), or the group has
// no route for it.
func (s *OtohaCatalogService) mediaCandidateReason(ctx context.Context, group *Group, c otohaMediaCandidate) string {
	switch c.Kind {
	case OtohaMediaKindVideo:
		if !otohaKnownVideoFamily(c.ModelID) {
			return OtohaImageReasonGatewayNotSupported
		}
	case OtohaMediaKindQuick:
		if !otohaContains(otohaQuickModelIDs, c.ModelID) {
			return OtohaImageReasonGatewayNotSupported
		}
	}
	if c.Kind != OtohaMediaKindVoice && group.ModelAllowlistEnabled() && !group.ModelAllowlist.Allows(c.ModelID) {
		return OtohaImageReasonBlockedByGroupModels
	}
	if s.media.routing != nil {
		if route := s.media.routing.MediaRoute(ctx, group, c.Kind, c.ModelID); route.Problem != "" {
			return route.Problem
		}
	}
	return ""
}

// ---- reading the group's accounts ----

// otohaMediaAccountSource lists, for each active Grok, Gemini and TypeSafe account of a group, what it offers. A Grok
// or Gemini account's model list is read; a TypeSafe account has none.
type otohaMediaAccountSource struct {
	accounts AccountRepository
	lister   otohaAccountModelLister
}

// NewOtohaMediaAccountSource reads the group's Grok accounts' model lists with lister (nil: the gateway's own table
// is used for every account) and takes the TypeSafe accounts as they are.
func NewOtohaMediaAccountSource(accounts AccountRepository, lister otohaAccountModelLister) OtohaMediaAccountSource {
	return &otohaMediaAccountSource{accounts: accounts, lister: lister}
}

func (s *otohaMediaAccountSource) ListGroupMediaAccounts(ctx context.Context, groupID int64) ([]OtohaAccountListing, error) {
	accounts, err := s.accounts.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	var asked []*Account
	for i := range accounts {
		if accounts[i].Platform == PlatformGrok || accounts[i].Platform == PlatformGemini || accounts[i].Platform == PlatformTypeSafe {
			asked = append(asked, &accounts[i])
		}
	}
	listings := make([]OtohaAccountListing, len(asked))
	var wg sync.WaitGroup
	slots := make(chan struct{}, otohaImageListConcurrency)
	for i, account := range asked {
		listings[i] = OtohaAccountListing{Account: account}
		if account.IsTypeSafe() {
			continue
		}
		if s.lister == nil {
			listings[i].Err = OtohaImageListUnsupported
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			listCtx, cancel := context.WithTimeout(ctx, otohaImageListTimeout)
			defer cancel()
			models, listErr := s.lister.ListOtohaModels(listCtx, account)
			listings[i].Models, listings[i].Err = models, listErr
		}()
	}
	wg.Wait()
	return listings, nil
}

// ---- keeping the report ----

type settingOtohaMediaDiscoveryStore struct {
	settings SettingRepository
}

// NewOtohaMediaDiscoveryStore keeps the last check's report of each group in the settings.
func NewOtohaMediaDiscoveryStore(settings SettingRepository) OtohaMediaDiscoveryStore {
	return &settingOtohaMediaDiscoveryStore{settings: settings}
}

func otohaMediaReportKey(groupID int64) string {
	return otohaMediaReportKeyPrefix + strconv.FormatInt(groupID, 10)
}

func (s *settingOtohaMediaDiscoveryStore) LoadMediaDiscovery(ctx context.Context, groupID int64) (*OtohaMediaDiscovery, error) {
	value, err := s.settings.GetValue(ctx, otohaMediaReportKey(groupID))
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var report OtohaMediaDiscovery
	if err := json.Unmarshal([]byte(value), &report); err != nil {
		return nil, fmt.Errorf("read the last video, voice and quick-judgement check: %w", err)
	}
	if report.Accounts == nil {
		report.Accounts = []OtohaMediaDiscoveryAccount{}
	}
	if report.Candidates == nil {
		report.Candidates = []OtohaMediaDiscoveryCandidate{}
	}
	if report.Missing == nil {
		report.Missing = []string{}
	}
	return &report, nil
}

func (s *settingOtohaMediaDiscoveryStore) SaveMediaDiscovery(ctx context.Context, groupID int64, report OtohaMediaDiscovery) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, otohaMediaReportKey(groupID), string(body))
}
