package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// otohaClaimBodyLimit bounds the public claim body: a code is 23 characters.
const otohaClaimBodyLimit = 4 << 10

type otohaClaims interface {
	CreateClaim(ctx context.Context, userID int64) (*service.OtohaClaimCode, error)
	RedeemClaim(ctx context.Context, code string) (*service.OtohaConfigFile, error)
	ConfigForUser(ctx context.Context, userID int64) (*service.OtohaConfigFile, error)
	AccountSummary(ctx context.Context, userID int64) (*service.OtohaAccount, error)
}

// OtohaHandler serves the Otoha purchase pages and the app's claim exchange (TASK-55).
type OtohaHandler struct {
	otoha otohaClaims
}

// NewOtohaHandler creates the Otoha handler.
func NewOtohaHandler(otohaService *service.OtohaService) *OtohaHandler {
	return newOtohaHandler(otohaService)
}

func newOtohaHandler(claims otohaClaims) *OtohaHandler {
	return &OtohaHandler{otoha: claims}
}

// CreateClaim issues a one-time code for the logged-in user.
// POST /api/v1/otoha/claims
func (h *OtohaHandler) CreateClaim(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	c.Header("Cache-Control", "no-store")
	claim, err := h.otoha.CreateClaim(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, claim)
}

type otohaRedeemRequest struct {
	Code string `json:"code"`
}

// RedeemClaim exchanges a code for the app configuration. The answer is the configuration file itself, so the
// app reads it like an imported file. A malformed body fails like a wrong code.
// POST /api/v1/otoha/claim (public, rate-limited per IP)
func (h *OtohaHandler) RedeemClaim(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, otohaClaimBodyLimit)
	var req otohaRedeemRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil || req.Code == "" {
		response.ErrorFrom(c, service.ErrOtohaClaimInvalid)
		return
	}
	cfg, err := h.otoha.RedeemClaim(c.Request.Context(), req.Code)
	if response.ErrorFrom(c, err) {
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// DownloadConfig sends the logged-in user's configuration as config.json.
// GET /api/v1/otoha/config
func (h *OtohaHandler) DownloadConfig(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	c.Header("Cache-Control", "no-store")
	cfg, err := h.otoha.ConfigForUser(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Content-Disposition", `attachment; filename="config.json"`)
	c.Data(http.StatusOK, "application/json; charset=utf-8", append(body, '\n'))
}

// GetAccount returns what the Otoha account page shows.
// GET /api/v1/otoha/account
func (h *OtohaHandler) GetAccount(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	account, err := h.otoha.AccountSummary(c.Request.Context(), subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, account)
}
