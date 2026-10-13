package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Reading what the group's accounts list, and keeping the last report (TASK-76).

const (
	otohaImageListTimeout     = 20 * time.Second
	otohaImageListConcurrency = 4
	otohaImageReportKeyPrefix = "otoha_image_discovery_"
)

// otohaAccountModelLister reads one account's upstream model list.
type otohaAccountModelLister interface {
	ListOtohaModels(ctx context.Context, account *Account) ([]OtohaListedModel, string)
}

// otohaAccountListingSource lists, for each active OpenAI, Grok and Gemini account of a group, what its upstream
// offers. Accounts of other providers draw nothing the gateway serves and are left out.
type otohaAccountListingSource struct {
	accounts AccountRepository
	lister   otohaAccountModelLister
}

// NewOtohaImageListingSource reads the group's accounts' model lists through the account test service.
func NewOtohaImageListingSource(accounts AccountRepository, tester *AccountTestService) OtohaImageListingSource {
	return &otohaAccountListingSource{accounts: accounts, lister: tester}
}

func (s *otohaAccountListingSource) ListGroupAccountModels(ctx context.Context, groupID int64) ([]OtohaAccountListing, error) {
	accounts, err := s.accounts.ListByGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	var drawing []*Account
	for i := range accounts {
		if accounts[i].Platform == PlatformOpenAI || accounts[i].Platform == PlatformGrok || accounts[i].Platform == PlatformGemini {
			drawing = append(drawing, &accounts[i])
		}
	}
	listings := make([]OtohaAccountListing, len(drawing))
	var wg sync.WaitGroup
	slots := make(chan struct{}, otohaImageListConcurrency)
	for i, account := range drawing {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			listCtx, cancel := context.WithTimeout(ctx, otohaImageListTimeout)
			defer cancel()
			models, listErr := s.lister.ListOtohaModels(listCtx, account)
			listings[i] = OtohaAccountListing{Account: account, Models: models, Err: listErr}
		}()
	}
	wg.Wait()
	return listings, nil
}

// ListOtohaModels reads the account's upstream model list, with what it says about each model's kind. A Grok
// account's list of image generation models (xAI keeps it apart from /v1/models) is read as well. The second result
// is empty when the list was read, else OtohaImageListUnsupported or OtohaImageListFailed. Nothing is saved.
func (s *AccountTestService) ListOtohaModels(ctx context.Context, account *Account) ([]OtohaListedModel, string) {
	ids, body, err := s.fetchUpstreamModelList(ctx, account)
	failure := ""
	var models []OtohaListedModel
	switch {
	case err != nil:
		failure = OtohaImageListFailed
		var syncErr *UpstreamModelSyncError
		if errors.As(err, &syncErr) && syncErr.Kind == UpstreamModelSyncErrorUnsupported {
			failure = OtohaImageListUnsupported
		}
	case len(body) > 0:
		models = parseOtohaListedModels(body, false)
	default:
		// A list read without its body (a Vertex service account's) still names the models.
		for _, id := range ids {
			models = append(models, OtohaListedModel{ID: strings.TrimPrefix(id, "models/")})
		}
	}
	if account.IsGrok() {
		if imageBody := s.fetchGrokImageGenerationModels(ctx, account); len(imageBody) > 0 {
			models = mergeOtohaListedModels(models, parseOtohaListedModels(imageBody, true))
			failure = ""
		}
	}
	return models, failure
}

// mergeOtohaListedModels adds the models of extra to models; where both name a model, the one that says it is an
// image model wins.
func mergeOtohaListedModels(models, extra []OtohaListedModel) []OtohaListedModel {
	index := make(map[string]int, len(models))
	for i, m := range models {
		index[m.ID] = i
	}
	for _, m := range extra {
		if i, ok := index[m.ID]; ok {
			if m.Kind == OtohaListedKindImage {
				models[i] = m
			}
			continue
		}
		index[m.ID] = len(models)
		models = append(models, m)
	}
	return models
}

// fetchGrokImageGenerationModels reads xAI's list of image generation models; nil when the account's endpoint does
// not give one.
func (s *AccountTestService) fetchGrokImageGenerationModels(ctx context.Context, account *Account) []byte {
	if s == nil || s.httpUpstream == nil {
		return nil
	}
	req, err := s.buildGrokUpstreamModelsRequest(ctx, account)
	if err != nil {
		return nil
	}
	req = grokImageGenerationModelsRequest(req)
	resp, err := s.doUpstreamModelsRequest(req, upstreamModelsProxyURL(account), account)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	limit := resolveModelsListReadLimit(s.cfg)
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil
	}
	return body
}

// grokImageGenerationModelsRequest turns a request for xAI's /v1/models into one for /v1/image-generation-models on
// the same host with the same headers.
func grokImageGenerationModelsRequest(req *http.Request) *http.Request {
	clone := req.Clone(req.Context())
	target := *req.URL
	path := strings.TrimRight(target.Path, "/")
	target.Path = strings.TrimSuffix(path, "/models") + "/image-generation-models"
	target.RawPath = ""
	clone.URL = &target
	return clone
}

// ---- the report ----

// settingOtohaImageDiscoveryStore keeps a group's last report in the settings.
type settingOtohaImageDiscoveryStore struct {
	settings SettingRepository
}

// NewOtohaImageDiscoveryStore keeps the last check's report of each group in the settings.
func NewOtohaImageDiscoveryStore(settings SettingRepository) OtohaImageDiscoveryStore {
	return &settingOtohaImageDiscoveryStore{settings: settings}
}

func otohaImageReportKey(groupID int64) string {
	return otohaImageReportKeyPrefix + strconv.FormatInt(groupID, 10)
}

func (s *settingOtohaImageDiscoveryStore) LoadImageDiscovery(ctx context.Context, groupID int64) (*OtohaImageDiscovery, error) {
	value, err := s.settings.GetValue(ctx, otohaImageReportKey(groupID))
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var report OtohaImageDiscovery
	if err := json.Unmarshal([]byte(value), &report); err != nil {
		return nil, fmt.Errorf("read the last image model check: %w", err)
	}
	if report.Accounts == nil {
		report.Accounts = []OtohaImageDiscoveryAccount{}
	}
	if report.Candidates == nil {
		report.Candidates = []OtohaImageDiscoveryCandidate{}
	}
	if report.Missing == nil {
		report.Missing = []string{}
	}
	return &report, nil
}

func (s *settingOtohaImageDiscoveryStore) SaveImageDiscovery(ctx context.Context, groupID int64, report OtohaImageDiscovery) error {
	body, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return s.settings.Set(ctx, otohaImageReportKey(groupID), string(body))
}

// ---- the scheduled check ----

const (
	otohaImageDiscoveryInterval   = 6 * time.Hour
	otohaImageDiscoveryFirstDelay = time.Minute
	otohaImageDiscoveryRunTimeout = 2 * time.Minute
)

// OtohaImageDiscoveryRunner checks the Otoha group's accounts for image (and video, voice and quick-judgement) models
// now and then, so the catalog follows
// what the upstream offers without the admin pressing a button.
type OtohaImageDiscoveryRunner struct {
	catalog  *OtohaCatalogService
	groupID  int64
	interval time.Duration
	delay    time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewOtohaImageDiscoveryRunner makes the runner for the group the server serves the Otoha app from.
func NewOtohaImageDiscoveryRunner(catalog *OtohaCatalogService, groupID int64) *OtohaImageDiscoveryRunner {
	return &OtohaImageDiscoveryRunner{
		catalog: catalog, groupID: groupID, interval: otohaImageDiscoveryInterval,
		delay: otohaImageDiscoveryFirstDelay, stopCh: make(chan struct{}),
	}
}

// Start begins checking; it does nothing when no Otoha group is set.
func (r *OtohaImageDiscoveryRunner) Start() {
	if r == nil || r.catalog == nil || r.groupID <= 0 {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				r.runOnce()
				timer.Reset(r.interval)
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *OtohaImageDiscoveryRunner) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), otohaImageDiscoveryRunTimeout)
	defer cancel()
	go func() {
		select {
		case <-r.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	if _, err := r.catalog.DiscoverImages(ctx, r.groupID, OtohaImageTriggerSchedule); err != nil {
		logger.L().Warn("otoha_catalog.image_discovery_failed", zap.Int64("group_id", r.groupID), zap.Error(err))
	}
	// The video, voice and quick-judgement models follow the same accounts (TASK-81).
	if _, err := r.catalog.DiscoverMedia(ctx, r.groupID, OtohaImageTriggerSchedule); err != nil && !errors.Is(err, ErrOtohaMediaDiscoveryUnavailable) {
		logger.L().Warn("otoha_catalog.media_discovery_failed", zap.Int64("group_id", r.groupID), zap.Error(err))
	}
}

// Stop ends the checks and waits for a running one.
func (r *OtohaImageDiscoveryRunner) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() { close(r.stopCh) })
	r.wg.Wait()
}
