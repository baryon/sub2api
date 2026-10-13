package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type otohaCatalogAdminStub struct {
	groupID  int64
	entryID  int64
	input    service.OtohaCatalogEntryInput
	order    []int64
	prefill  string
	err      error
	view     *service.OtohaCatalogAdminView
	lastCall string
	imageIn  service.OtohaImageEntryInput
	trigger  string
}

func (s *otohaCatalogAdminStub) AdminView(_ context.Context, groupID int64) (*service.OtohaCatalogAdminView, error) {
	s.groupID, s.lastCall = groupID, "view"
	return s.view, s.err
}

func (s *otohaCatalogAdminStub) CreateEntry(_ context.Context, groupID int64, input service.OtohaCatalogEntryInput) (*service.OtohaCatalogEntry, error) {
	s.groupID, s.input, s.lastCall = groupID, input, "create"
	if s.err != nil {
		return nil, s.err
	}
	return &service.OtohaCatalogEntry{ID: 11, GroupID: groupID, ModelID: input.ModelID}, nil
}

func (s *otohaCatalogAdminStub) UpdateEntry(_ context.Context, groupID, entryID int64, input service.OtohaCatalogEntryInput) (*service.OtohaCatalogEntry, error) {
	s.groupID, s.entryID, s.input, s.lastCall = groupID, entryID, input, "update"
	if s.err != nil {
		return nil, s.err
	}
	return &service.OtohaCatalogEntry{ID: entryID, GroupID: groupID, ModelID: input.ModelID}, nil
}

func (s *otohaCatalogAdminStub) DeleteEntry(_ context.Context, groupID, entryID int64) error {
	s.groupID, s.entryID, s.lastCall = groupID, entryID, "delete"
	return s.err
}

func (s *otohaCatalogAdminStub) ReorderEntries(_ context.Context, groupID int64, entryIDs []int64) error {
	s.groupID, s.order, s.lastCall = groupID, entryIDs, "order"
	return s.err
}

func (s *otohaCatalogAdminStub) Prefill(_ context.Context, groupID int64, modelID string) (*service.OtohaCatalogPrefill, error) {
	s.groupID, s.prefill, s.lastCall = groupID, modelID, "prefill"
	if s.err != nil {
		return nil, s.err
	}
	return &service.OtohaCatalogPrefill{Entry: service.OtohaCatalogEntry{ModelID: modelID}}, nil
}

func (s *otohaCatalogAdminStub) CreateImageEntry(_ context.Context, groupID int64, input service.OtohaImageEntryInput) (*service.OtohaImageEntry, error) {
	s.groupID, s.imageIn, s.lastCall = groupID, input, "create-image"
	if s.err != nil {
		return nil, s.err
	}
	return &service.OtohaImageEntry{ID: 21, GroupID: groupID, ModelID: input.ModelID}, nil
}

func (s *otohaCatalogAdminStub) UpdateImageEntry(_ context.Context, groupID, entryID int64, input service.OtohaImageEntryInput) (*service.OtohaImageEntry, error) {
	s.groupID, s.entryID, s.imageIn, s.lastCall = groupID, entryID, input, "update-image"
	if s.err != nil {
		return nil, s.err
	}
	return &service.OtohaImageEntry{ID: entryID, GroupID: groupID, ModelID: input.ModelID}, nil
}

func (s *otohaCatalogAdminStub) DeleteImageEntry(_ context.Context, groupID, entryID int64) error {
	s.groupID, s.entryID, s.lastCall = groupID, entryID, "delete-image"
	return s.err
}

func (s *otohaCatalogAdminStub) DiscoverImages(_ context.Context, groupID int64, trigger string) (*service.OtohaImageDiscovery, error) {
	s.groupID, s.trigger, s.lastCall = groupID, trigger, "discover-images"
	if s.err != nil {
		return nil, s.err
	}
	return &service.OtohaImageDiscovery{Trigger: trigger, Accounts: []service.OtohaImageDiscoveryAccount{}, Candidates: []service.OtohaImageDiscoveryCandidate{}, Missing: []string{}}, nil
}

func otohaAdminRouter(stub *otohaCatalogAdminStub) *gin.Engine {
	return otohaAdminRouterFor(stub, 0)
}

func otohaAdminRouterFor(stub *otohaCatalogAdminStub, otohaGroupID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := newOtohaCatalogHandler(stub, otohaGroupID)
	r := gin.New()
	r.GET("/otoha-catalog/settings", h.Settings)
	g := r.Group("/groups/:id/otoha-catalog")
	g.GET("", h.Get)
	g.POST("/entries", h.CreateEntry)
	g.PUT("/entries/:entry_id", h.UpdateEntry)
	g.DELETE("/entries/:entry_id", h.DeleteEntry)
	g.PUT("/order", h.Reorder)
	g.POST("/prefill", h.Prefill)
	g.POST("/image-entries", h.CreateImageEntry)
	g.PUT("/image-entries/:entry_id", h.UpdateImageEntry)
	g.DELETE("/image-entries/:entry_id", h.DeleteImageEntry)
	g.POST("/image-discovery", h.DiscoverImages)
	return r
}

func otohaAdminDo(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestOtohaCatalogAdminCreateTakesEveryField(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/entries", `{
		"model_id":"gpt-6-luna","name":"Luna","description":"d","enabled":false,"inputs":["text","image"],"tools":true,
		"context":1000,"max_output":100,"reasoning":["low","high"],"default_reasoning":"low","speed":"fast",
		"strengths":{"coding":"strong"},"complexity":"complex","roles":["lead"],"use":["coding"],"profile_source":"vendor",
		"cost_tier":"high","api":"anthropic-messages"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, int64(7), stub.groupID)
	in := stub.input
	require.Equal(t, "gpt-6-luna", in.ModelID)
	require.Equal(t, "Luna", in.Name)
	require.Equal(t, "d", in.Description)
	require.False(t, in.Enabled)
	require.Equal(t, []string{"text", "image"}, in.Inputs)
	require.True(t, in.Tools)
	require.Equal(t, 1000, in.Context)
	require.Equal(t, 100, in.MaxOutput)
	require.Equal(t, []string{"low", "high"}, in.Reasoning)
	require.Equal(t, "low", in.DefaultReasoning)
	require.Equal(t, "fast", in.Speed)
	require.Equal(t, map[string]string{"coding": "strong"}, in.Strengths)
	require.Equal(t, "complex", in.Complexity)
	require.Equal(t, []string{"lead"}, in.Roles)
	require.Equal(t, []string{"coding"}, in.Use)
	require.Equal(t, "vendor", in.ProfileSource)
	require.Equal(t, "high", in.CostTier)
	require.Equal(t, "anthropic-messages", in.API)

	var body struct {
		Data service.OtohaCatalogEntry `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, int64(11), body.Data.ID)
}

func TestOtohaCatalogAdminCreateIsEnabledUnlessSaidOtherwise(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/entries", `{"model_id":"gpt-6-luna"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	require.True(t, stub.input.Enabled)
}

func TestOtohaCatalogAdminRejectsBadRequests(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	r := otohaAdminRouter(stub)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/groups/abc/otoha-catalog", ""},
		{http.MethodGet, "/groups/0/otoha-catalog", ""},
		{http.MethodPost, "/groups/7/otoha-catalog/entries", `{"model_id":`},
		{http.MethodPost, "/groups/7/otoha-catalog/entries", `{"context":"big"}`},
		{http.MethodPut, "/groups/7/otoha-catalog/entries/x", `{"model_id":"a"}`},
		{http.MethodDelete, "/groups/7/otoha-catalog/entries/-1", ""},
		{http.MethodPut, "/groups/7/otoha-catalog/order", `{"entry_ids":"1,2"}`},
		{http.MethodPost, "/groups/7/otoha-catalog/prefill", `{}`},
	} {
		rec := otohaAdminDo(r, tc.method, tc.path, tc.body)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%s %s %s", tc.method, tc.path, tc.body)
	}
	require.Empty(t, stub.lastCall, "nothing reached the catalog")
}

func TestOtohaCatalogAdminPassesIDsAndServiceErrors(t *testing.T) {
	stub := &otohaCatalogAdminStub{view: &service.OtohaCatalogAdminView{GroupID: 7, Entries: []service.OtohaCatalogAdminEntry{}}}
	r := otohaAdminRouter(stub)

	require.Equal(t, http.StatusOK, otohaAdminDo(r, http.MethodGet, "/groups/7/otoha-catalog", "").Code)
	require.Equal(t, "view", stub.lastCall)

	require.Equal(t, http.StatusOK, otohaAdminDo(r, http.MethodPut, "/groups/7/otoha-catalog/entries/12", `{"model_id":"a","enabled":true}`).Code)
	require.Equal(t, int64(12), stub.entryID)
	require.True(t, stub.input.Enabled)

	require.Equal(t, http.StatusOK, otohaAdminDo(r, http.MethodDelete, "/groups/7/otoha-catalog/entries/12", "").Code)
	require.Equal(t, "delete", stub.lastCall)

	require.Equal(t, http.StatusOK, otohaAdminDo(r, http.MethodPut, "/groups/7/otoha-catalog/order", `{"entry_ids":[3,1,2]}`).Code)
	require.Equal(t, []int64{3, 1, 2}, stub.order)

	require.Equal(t, http.StatusOK, otohaAdminDo(r, http.MethodPost, "/groups/7/otoha-catalog/prefill", `{"model_id":"gpt-6-luna"}`).Code)
	require.Equal(t, "gpt-6-luna", stub.prefill)

	stub.err = service.ErrOtohaCatalogEntryNotFound
	require.Equal(t, http.StatusNotFound, otohaAdminDo(r, http.MethodDelete, "/groups/7/otoha-catalog/entries/99", "").Code)
	stub.err = service.ErrOtohaCatalogEntryExists
	require.Equal(t, http.StatusConflict, otohaAdminDo(r, http.MethodPost, "/groups/7/otoha-catalog/entries", `{"model_id":"a"}`).Code)
	stub.err = service.ErrGroupNotFound
	require.Equal(t, http.StatusNotFound, otohaAdminDo(r, http.MethodGet, "/groups/8/otoha-catalog", "").Code)
}

// The admin's catalog page opens on the group the server serves the Otoha app from.
func TestOtohaCatalogAdminSettingsNameTheOtohaGroup(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouterFor(stub, 12), http.MethodGet, "/otoha-catalog/settings", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"otoha_group_id":12}`, otohaAdminData(t, rec))

	rec = otohaAdminDo(otohaAdminRouterFor(stub, 0), http.MethodGet, "/otoha-catalog/settings", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"otoha_group_id":0}`, otohaAdminData(t, rec), "0: the server has no Otoha group configured")
	require.Empty(t, stub.lastCall)
}

func otohaAdminData(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return string(body.Data)
}

func TestOtohaCatalogAdminTakesPriceTiersAndCompaction(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPut, "/groups/7/otoha-catalog/entries/3", `{
		"model_id":"claude-haiku-5-5",
		"price_tiers":[{"aboveInputTokens":0,"inclusive":false,"input":1,"output":5},
			{"aboveInputTokens":100000,"inclusive":false,"input":5,"output":25,"cachedInput":0.5}],
		"compaction":{"kind":"anthropic","minTrigger":50000}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	cached := 0.5
	require.Equal(t, []service.OtohaPriceTier{
		{Input: 1, Output: 5},
		{AboveInputTokens: 100000, Input: 5, Output: 25, CachedInput: &cached},
	}, stub.input.PriceTiers)
	require.Equal(t, service.OtohaCompaction{Kind: "anthropic", MinTrigger: 50000}, stub.input.Compaction)
}

func TestOtohaCatalogAdminTakesThinkingAndLevels(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPut, "/groups/7/otoha-catalog/entries/3", `{
		"model_id":"claude-haiku-5-5",
		"thinking":{"canBeOff":true,"settings":[{"value":"off","maxEffort":"high"},{"value":"adaptive"}]},
		"levels":[{"setting":"off","complexity":"simple","extraOutput":"low"}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	yes := true
	require.Equal(t, &service.OtohaThinking{CanBeOff: &yes, Settings: []service.OtohaThinkingSetting{
		{Value: "off", MaxEffort: "high"}, {Value: "adaptive"},
	}}, stub.input.Thinking)
	require.Equal(t, []service.OtohaThinkingLevel{{Setting: "off", Complexity: "simple", ExtraOutput: "low"}}, stub.input.Levels)
}

func TestOtohaCatalogAdminImageEntryTakesEveryField(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/image-entries", `{
		"model_id":"gpt-image-2","name":"GPT Image 2","description":"d","enabled":false,
		"sizes":["1024x1024","1536x1024"],"qualities":["low","high"],"edits":true,
		"price_1k":0.05,"price_4k":0.2}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "create-image", stub.lastCall)
	require.Equal(t, int64(7), stub.groupID)
	in := stub.imageIn
	require.Equal(t, "gpt-image-2", in.ModelID)
	require.Equal(t, "GPT Image 2", in.Name)
	require.Equal(t, "d", in.Description)
	require.False(t, in.Enabled)
	require.Equal(t, []string{"1024x1024", "1536x1024"}, in.Sizes)
	require.Equal(t, []string{"low", "high"}, in.Qualities)
	require.True(t, in.Edits)
	require.NotNil(t, in.Price1K)
	require.Equal(t, 0.05, *in.Price1K)
	require.Nil(t, in.Price2K, "a price left out follows billing")
	require.NotNil(t, in.Price4K)
	require.Equal(t, 0.2, *in.Price4K)
}

func TestOtohaCatalogAdminImageEntryIsEnabledUnlessTurnedOff(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/image-entries", `{"model_id":"gpt-image-2","sizes":["1024x1024"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.True(t, stub.imageIn.Enabled)
}

func TestOtohaCatalogAdminImageEntryUpdateAndDelete(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	r := otohaAdminRouter(stub)

	rec := otohaAdminDo(r, http.MethodPut, "/groups/7/otoha-catalog/image-entries/21", `{"model_id":"gpt-image-2","sizes":["1024x1024"],"edits":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "update-image", stub.lastCall)
	require.Equal(t, int64(21), stub.entryID)
	require.True(t, stub.imageIn.Edits)

	rec = otohaAdminDo(r, http.MethodDelete, "/groups/7/otoha-catalog/image-entries/21", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "delete-image", stub.lastCall)
	require.Equal(t, int64(21), stub.entryID)
}

func TestOtohaCatalogAdminImageEntryRefusesABadBody(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/image-entries", `{"sizes":"not a list"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, stub.lastCall)
}

func TestOtohaCatalogAdminViewCarriesTheImageEntries(t *testing.T) {
	stub := &otohaCatalogAdminStub{view: &service.OtohaCatalogAdminView{
		ImageEntries: []service.OtohaImageAdminEntry{{
			OtohaImageEntry: service.OtohaImageEntry{ID: 21, ModelID: "gpt-image-2", Sizes: []string{"1024x1024"}},
			Prices:          []service.OtohaImageTierPrice{{Tier: "1K", Upstream: 0.04, Sale: 0.06}},
			InCatalog:       true,
		}},
	}}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodGet, "/groups/7/otoha-catalog", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Data struct {
			ImageEntries []map[string]any `json:"image_entries"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data.ImageEntries, 1)
	entry := body.Data.ImageEntries[0]
	require.Equal(t, "gpt-image-2", entry["model_id"])
	require.Equal(t, true, entry["in_catalog"])
	require.Equal(t, "", entry["problem"])
	require.Equal(t, []any{map[string]any{"tier": "1K", "upstream": 0.04, "sale": 0.06}}, entry["prices"])
}

func TestOtohaCatalogAdminImageDiscoveryRunsForTheGroupAsAnAdminCheck(t *testing.T) {
	stub := &otohaCatalogAdminStub{}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/image-discovery", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "discover-images", stub.lastCall)
	require.Equal(t, int64(7), stub.groupID)
	require.Equal(t, service.OtohaImageTriggerAdmin, stub.trigger)
	require.Contains(t, rec.Body.String(), `"candidates":[]`)
}

func TestOtohaCatalogAdminImageDiscoveryErrorsAreMappedNotLeaked(t *testing.T) {
	stub := &otohaCatalogAdminStub{err: service.ErrOtohaImageDiscoveryUnavailable}
	rec := otohaAdminDo(otohaAdminRouter(stub), http.MethodPost, "/groups/7/otoha-catalog/image-discovery", "")
	require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
	require.NotContains(t, rec.Body.String(), "panic")

	bad := otohaAdminDo(otohaAdminRouter(&otohaCatalogAdminStub{}), http.MethodPost, "/groups/abc/otoha-catalog/image-discovery", "")
	require.Equal(t, http.StatusBadRequest, bad.Code)
}
