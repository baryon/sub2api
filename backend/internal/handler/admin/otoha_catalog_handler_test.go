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
