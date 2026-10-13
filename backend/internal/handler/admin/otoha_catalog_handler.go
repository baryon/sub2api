package admin

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// otohaCatalogAdmin is what the admin page does with a group's Otoha model catalog (TASK-54).
type otohaCatalogAdmin interface {
	AdminView(ctx context.Context, groupID int64) (*service.OtohaCatalogAdminView, error)
	CreateEntry(ctx context.Context, groupID int64, input service.OtohaCatalogEntryInput) (*service.OtohaCatalogEntry, error)
	UpdateEntry(ctx context.Context, groupID, entryID int64, input service.OtohaCatalogEntryInput) (*service.OtohaCatalogEntry, error)
	DeleteEntry(ctx context.Context, groupID, entryID int64) error
	ReorderEntries(ctx context.Context, groupID int64, entryIDs []int64) error
	Prefill(ctx context.Context, groupID int64, modelID string) (*service.OtohaCatalogPrefill, error)
	CreateImageEntry(ctx context.Context, groupID int64, input service.OtohaImageEntryInput) (*service.OtohaImageEntry, error)
	UpdateImageEntry(ctx context.Context, groupID, entryID int64, input service.OtohaImageEntryInput) (*service.OtohaImageEntry, error)
	DeleteImageEntry(ctx context.Context, groupID, entryID int64) error
	DiscoverImages(ctx context.Context, groupID int64, trigger string) (*service.OtohaImageDiscovery, error)
	UpdateMediaEntry(ctx context.Context, groupID int64, input service.OtohaMediaEntryInput) (*service.OtohaMediaEntry, error)
	DiscoverMedia(ctx context.Context, groupID int64, trigger string) (*service.OtohaMediaDiscovery, error)
}

// OtohaCatalogHandler serves the admin API for a group's Otoha model catalog.
type OtohaCatalogHandler struct {
	catalog otohaCatalogAdmin
	// otohaGroupID is the group the server serves the Otoha app from (otoha.group_id); 0 when not configured.
	otohaGroupID int64
}

// NewOtohaCatalogHandler creates the handler.
func NewOtohaCatalogHandler(catalog *service.OtohaCatalogService, cfg *config.Config) *OtohaCatalogHandler {
	var otohaGroupID int64
	if cfg != nil {
		otohaGroupID = cfg.Otoha.GroupID
	}
	return newOtohaCatalogHandler(catalog, otohaGroupID)
}

func newOtohaCatalogHandler(catalog otohaCatalogAdmin, otohaGroupID int64) *OtohaCatalogHandler {
	return &OtohaCatalogHandler{catalog: catalog, otohaGroupID: otohaGroupID}
}

// Settings tells the admin's catalog page which group the Otoha app is served from (TASK-63); 0 when none is set.
// GET /api/v1/admin/otoha-catalog/settings
func (h *OtohaCatalogHandler) Settings(c *gin.Context) {
	response.Success(c, gin.H{"otoha_group_id": h.otohaGroupID})
}

// OtohaCatalogEntryRequest is one catalog entry as the admin page submits it.
type OtohaCatalogEntryRequest struct {
	ModelID          string            `json:"model_id"`
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Enabled          *bool             `json:"enabled"`
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
	CostTier         string            `json:"cost_tier"`
	API              string            `json:"api"`
	// PriceTiers overrides the price tiers derived from billing; empty derives them (TASK-67).
	PriceTiers []service.OtohaPriceTier `json:"price_tiers"`
	Compaction service.OtohaCompaction  `json:"compaction"`
	// Thinking and Levels are the thinking the model accepts and its profile per level (TASK-70).
	Thinking *service.OtohaThinking       `json:"thinking"`
	Levels   []service.OtohaThinkingLevel `json:"levels"`
}

func (r OtohaCatalogEntryRequest) input() service.OtohaCatalogEntryInput {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return service.OtohaCatalogEntryInput{
		ModelID:          r.ModelID,
		Name:             r.Name,
		Description:      r.Description,
		Enabled:          enabled,
		Inputs:           r.Inputs,
		Tools:            r.Tools,
		Context:          r.Context,
		MaxOutput:        r.MaxOutput,
		Reasoning:        r.Reasoning,
		DefaultReasoning: r.DefaultReasoning,
		Speed:            r.Speed,
		Strengths:        r.Strengths,
		Complexity:       r.Complexity,
		Roles:            r.Roles,
		Use:              r.Use,
		ProfileSource:    r.ProfileSource,
		CostTier:         r.CostTier,
		API:              r.API,
		PriceTiers:       r.PriceTiers,
		Compaction:       r.Compaction,
		Thinking:         r.Thinking,
		Levels:           r.Levels,
	}
}

type otohaCatalogOrderRequest struct {
	EntryIDs []int64 `json:"entry_ids"`
}

type otohaCatalogPrefillRequest struct {
	ModelID string `json:"model_id"`
}

// Get lists a group's catalog with prices, problems and the app's preview.
// GET /api/v1/admin/groups/:id/otoha-catalog
func (h *OtohaCatalogHandler) Get(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	view, err := h.catalog.AdminView(c.Request.Context(), groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}

// CreateEntry adds a model to the group's catalog.
// POST /api/v1/admin/groups/:id/otoha-catalog/entries
func (h *OtohaCatalogHandler) CreateEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	var req OtohaCatalogEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	entry, err := h.catalog.CreateEntry(c.Request.Context(), groupID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, entry)
}

// UpdateEntry replaces one entry of the group's catalog.
// PUT /api/v1/admin/groups/:id/otoha-catalog/entries/:entry_id
func (h *OtohaCatalogHandler) UpdateEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	entryID, ok := parsePositiveIDParam(c, "entry_id")
	if !ok {
		return
	}
	var req OtohaCatalogEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	entry, err := h.catalog.UpdateEntry(c.Request.Context(), groupID, entryID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, entry)
}

// DeleteEntry removes one entry of the group's catalog.
// DELETE /api/v1/admin/groups/:id/otoha-catalog/entries/:entry_id
func (h *OtohaCatalogHandler) DeleteEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	entryID, ok := parsePositiveIDParam(c, "entry_id")
	if !ok {
		return
	}
	if err := h.catalog.DeleteEntry(c.Request.Context(), groupID, entryID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// Reorder puts the group's catalog in the given order.
// PUT /api/v1/admin/groups/:id/otoha-catalog/order
func (h *OtohaCatalogHandler) Reorder(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	var req otohaCatalogOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if err := h.catalog.ReorderEntries(c.Request.Context(), groupID, req.EntryIDs); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

// Prefill drafts an entry from upstream metadata and the price table; nothing is saved.
// POST /api/v1/admin/groups/:id/otoha-catalog/prefill
func (h *OtohaCatalogHandler) Prefill(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	var req otohaCatalogPrefillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ModelID) == "" {
		response.BadRequest(c, "model_id is required")
		return
	}
	draft, err := h.catalog.Prefill(c.Request.Context(), groupID, req.ModelID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, draft)
}

// OtohaImageEntryRequest is one image model as the admin page submits it (TASK-73). A price left out follows billing.
type OtohaImageEntryRequest struct {
	ModelID     string   `json:"model_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     *bool    `json:"enabled"`
	Sizes       []string `json:"sizes"`
	Qualities   []string `json:"qualities"`
	Edits       bool     `json:"edits"`
	Price1K     *float64 `json:"price_1k"`
	Price2K     *float64 `json:"price_2k"`
	Price4K     *float64 `json:"price_4k"`
}

func (r OtohaImageEntryRequest) input() service.OtohaImageEntryInput {
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return service.OtohaImageEntryInput{
		ModelID:     r.ModelID,
		Name:        r.Name,
		Description: r.Description,
		Enabled:     enabled,
		Sizes:       r.Sizes,
		Qualities:   r.Qualities,
		Edits:       r.Edits,
		Price1K:     r.Price1K,
		Price2K:     r.Price2K,
		Price4K:     r.Price4K,
	}
}

// CreateImageEntry adds an image model to the group's catalog.
// POST /api/v1/admin/groups/:id/otoha-catalog/image-entries
func (h *OtohaCatalogHandler) CreateImageEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	var req OtohaImageEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	entry, err := h.catalog.CreateImageEntry(c.Request.Context(), groupID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, entry)
}

// UpdateImageEntry replaces one image model of the group's catalog.
// PUT /api/v1/admin/groups/:id/otoha-catalog/image-entries/:entry_id
func (h *OtohaCatalogHandler) UpdateImageEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	entryID, ok := parsePositiveIDParam(c, "entry_id")
	if !ok {
		return
	}
	var req OtohaImageEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	entry, err := h.catalog.UpdateImageEntry(c.Request.Context(), groupID, entryID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, entry)
}

// DeleteImageEntry removes one image model of the group's catalog.
// DELETE /api/v1/admin/groups/:id/otoha-catalog/image-entries/:entry_id
func (h *OtohaCatalogHandler) DeleteImageEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	entryID, ok := parsePositiveIDParam(c, "entry_id")
	if !ok {
		return
	}
	if err := h.catalog.DeleteImageEntry(c.Request.Context(), groupID, entryID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// DiscoverImages checks the group's accounts for image models now and returns what it found.
// POST /api/v1/admin/groups/:id/otoha-catalog/image-discovery
func (h *OtohaCatalogHandler) DiscoverImages(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	report, err := h.catalog.DiscoverImages(c.Request.Context(), groupID, service.OtohaImageTriggerAdmin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}

// OtohaMediaEntryRequest is a video, voice or quick-judgement model as the admin page submits it (TASK-81): which one
// (kind and model), and the fields the admin can change. Entries are made by the check of the group's accounts, so
// there is no create or delete.
type OtohaMediaEntryRequest struct {
	Kind        string `json:"kind"`
	ModelID     string `json:"model_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     *bool  `json:"enabled"`
}

// UpdateMediaEntry changes the name, description and switch of one media model of the group's catalog.
// PUT /api/v1/admin/groups/:id/otoha-catalog/media-entries
func (h *OtohaCatalogHandler) UpdateMediaEntry(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	var req OtohaMediaEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request body: "+err.Error())
		return
	}
	switch req.Kind {
	case service.OtohaMediaKindVideo, service.OtohaMediaKindVoice, service.OtohaMediaKindQuick:
	default:
		response.BadRequest(c, "kind must be video, voice or quick")
		return
	}
	if strings.TrimSpace(req.ModelID) == "" {
		response.BadRequest(c, "model_id is required")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	entry, err := h.catalog.UpdateMediaEntry(c.Request.Context(), groupID, service.OtohaMediaEntryInput{
		Kind: req.Kind, ModelID: strings.TrimSpace(req.ModelID), Name: req.Name, Description: req.Description, Enabled: enabled,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, entry)
}

// DiscoverMedia checks the group's accounts for video, voice and quick-judgement models now and returns what it found.
// POST /api/v1/admin/groups/:id/otoha-catalog/media-discovery
func (h *OtohaCatalogHandler) DiscoverMedia(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	report, err := h.catalog.DiscoverMedia(c.Request.Context(), groupID, service.OtohaImageTriggerAdmin)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}
