package admin

import (
	"context"
	"strings"

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
}

// OtohaCatalogHandler serves the admin API for a group's Otoha model catalog.
type OtohaCatalogHandler struct {
	catalog otohaCatalogAdmin
}

// NewOtohaCatalogHandler creates the handler.
func NewOtohaCatalogHandler(catalog *service.OtohaCatalogService) *OtohaCatalogHandler {
	return newOtohaCatalogHandler(catalog)
}

func newOtohaCatalogHandler(catalog otohaCatalogAdmin) *OtohaCatalogHandler {
	return &OtohaCatalogHandler{catalog: catalog}
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
