package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// otohaAutoTopUpBodyLimit bounds the settings bodies: a few numbers or one id.
const otohaAutoTopUpBodyLimit = 4 << 10

type otohaAutoTopUp interface {
	StateForWebsite(ctx context.Context, userID int64) (*service.OtohaAutoTopUp, error)
	UpdateSettings(ctx context.Context, userID int64, in service.AutoTopUpSettings) (*service.OtohaAutoTopUp, error)
	StartCardSetup(ctx context.Context, userID int64) (*service.CardSetupStart, error)
	FinishCardSetup(ctx context.Context, userID int64, setupID string) (*service.OtohaAutoTopUp, error)
	RemoveCard(ctx context.Context, userID int64) (*service.OtohaAutoTopUp, error)
}

// autoTopUpUser returns the logged-in user and the automatic top-up service, or answers the request itself.
func (h *OtohaHandler) autoTopUpUser(c *gin.Context) (int64, otohaAutoTopUp, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return 0, nil, false
	}
	c.Header("Cache-Control", "no-store")
	if h.autoTopUp == nil {
		response.ErrorFrom(c, service.ErrAutoTopUpUnavailable)
		return 0, nil, false
	}
	return subject.UserID, h.autoTopUp, true
}

// GetAutoTopUp returns the user's automatic top-up settings, the saved card (brand and last four digits) and the
// latest top-up.
// GET /api/v1/otoha/auto-topup
func (h *OtohaHandler) GetAutoTopUp(c *gin.Context) {
	userID, auto, ok := h.autoTopUpUser(c)
	if !ok {
		return
	}
	state, err := auto.StateForWebsite(c.Request.Context(), userID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, state)
}

type otohaAutoTopUpRequest struct {
	Enabled       *bool    `json:"enabled"`
	ThresholdUSD  *float64 `json:"threshold_usd"`
	AmountUSD     *float64 `json:"amount_usd"`
	MonthlyCapUSD *float64 `json:"monthly_cap_usd"`
}

// UpdateAutoTopUp saves whether automatic top-up is on and its three amounts.
// PUT /api/v1/otoha/auto-topup
func (h *OtohaHandler) UpdateAutoTopUp(c *gin.Context) {
	userID, auto, ok := h.autoTopUpUser(c)
	if !ok {
		return
	}
	var req otohaAutoTopUpRequest
	if !decodeOtohaBody(c, &req) || req.Enabled == nil || req.ThresholdUSD == nil || req.AmountUSD == nil || req.MonthlyCapUSD == nil {
		response.BadRequest(c, "Invalid request: enabled, threshold_usd, amount_usd and monthly_cap_usd are required")
		return
	}
	state, err := auto.UpdateSettings(c.Request.Context(), userID, service.AutoTopUpSettings{
		Enabled: *req.Enabled, ThresholdUSD: *req.ThresholdUSD, AmountUSD: *req.AmountUSD, MonthlyCapUSD: *req.MonthlyCapUSD,
	})
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, state)
}

// StartAutoTopUpCardSetup begins saving a card; the answer is what the browser needs to show Stripe's card form.
// POST /api/v1/otoha/auto-topup/card-setup
func (h *OtohaHandler) StartAutoTopUpCardSetup(c *gin.Context) {
	userID, auto, ok := h.autoTopUpUser(c)
	if !ok {
		return
	}
	start, err := auto.StartCardSetup(c.Request.Context(), userID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, start)
}

type otohaAutoTopUpCardRequest struct {
	SetupID string `json:"setup_id"`
}

// FinishAutoTopUpCardSetup keeps the card the browser just confirmed with Stripe.
// POST /api/v1/otoha/auto-topup/card
func (h *OtohaHandler) FinishAutoTopUpCardSetup(c *gin.Context) {
	userID, auto, ok := h.autoTopUpUser(c)
	if !ok {
		return
	}
	var req otohaAutoTopUpCardRequest
	if !decodeOtohaBody(c, &req) || strings.TrimSpace(req.SetupID) == "" {
		response.BadRequest(c, "Invalid request: setup_id is required")
		return
	}
	state, err := auto.FinishCardSetup(c.Request.Context(), userID, strings.TrimSpace(req.SetupID))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, state)
}

// RemoveAutoTopUpCard forgets the saved card and turns automatic top-up off.
// DELETE /api/v1/otoha/auto-topup/card
func (h *OtohaHandler) RemoveAutoTopUpCard(c *gin.Context) {
	userID, auto, ok := h.autoTopUpUser(c)
	if !ok {
		return
	}
	state, err := auto.RemoveCard(c.Request.Context(), userID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, state)
}

func decodeOtohaBody(c *gin.Context, into any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, otohaAutoTopUpBodyLimit)
	return json.NewDecoder(c.Request.Body).Decode(into) == nil
}
