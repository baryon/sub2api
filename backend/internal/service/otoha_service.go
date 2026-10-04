package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// The Otoha service sells the Otoha AI app's model use (TASK-55): a payment for the Otoha group makes sure the
// user has one "Otoha Desktop" key there; the account page turns it into a one-time claim code, and the app
// exchanges the code for its configuration file (purchase design §5 items 4–6, §6, §9).

const (
	// OtohaDesktopKeyName names the one key the app uses; there is at most one per user in the Otoha group.
	OtohaDesktopKeyName = "Otoha Desktop"

	otohaClaimTTL          = 15 * time.Minute
	otohaClaimRetention    = 24 * time.Hour
	otohaClaimCodeLength   = 20
	otohaClaimGroupSize    = 5
	otohaClaimAlphabet     = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // Crockford base32: no I, L, O, U
	otohaConfigVersion     = 1
	otohaProviderKind      = "compatibleGateway"
	otohaCatalogRefreshFmt = "%s/v1/models?client=otoha"
	otohaOpenURLPrefix     = "otoha://claim?code="
)

var (
	ErrOtohaNotConfigured = infraerrors.New(http.StatusServiceUnavailable, "OTOHA_NOT_CONFIGURED",
		"Otoha service is not available on this site")
	ErrOtohaNoAccess = infraerrors.Forbidden("OTOHA_NO_ACCESS",
		"This account has no Otoha plan or balance yet. Buy a plan or add balance, then try again")
	ErrOtohaKeyDisabled = infraerrors.Forbidden("OTOHA_KEY_DISABLED",
		"The Otoha Desktop key on this account cannot be used: it is turned off, expired or out of quota. Check it under API keys, then try again")
	// ErrOtohaClaimInvalid is the one answer for every code that cannot be exchanged — wrong, used, expired, or
	// whose key or account has since changed — so the endpoint never tells whether a code existed.
	ErrOtohaClaimInvalid = infraerrors.BadRequest("OTOHA_CLAIM_INVALID",
		"This code cannot be used. It may be mistyped, already used or expired. Create a new code on your Otoha account page")

	// ErrOtohaClaimNotUsable is the repository's answer when no unused, unexpired claim has the hash.
	ErrOtohaClaimNotUsable = errors.New("otoha claim not usable")
)

// OtohaClaim is a stored claim code: only its SHA-256 is kept.
type OtohaClaim struct {
	ID        int64
	CodeHash  string
	UserID    int64
	APIKeyID  int64
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// OtohaPlanOrder is the user's latest completed plan order for the Otoha group.
type OtohaPlanOrder struct {
	PlanName  string
	Reference string
}

// OtohaRepository stores the Otoha key and claim codes.
type OtohaRepository interface {
	// EnsureNamedAPIKey returns the user's oldest undeleted key with this name in the group, or creates one with
	// candidateKey. Concurrent callers for the same user get the same key. created reports whether it was new.
	EnsureNamedAPIKey(ctx context.Context, userID, groupID int64, name, candidateKey string) (key *APIKey, created bool, err error)
	CreateClaim(ctx context.Context, claim *OtohaClaim) error
	// ConsumeClaim marks the unused claim with this hash as used if it has not expired at now, atomically, and
	// returns it; ErrOtohaClaimNotUsable when there is none.
	ConsumeClaim(ctx context.Context, codeHash string, now time.Time) (*OtohaClaim, error)
	DeleteExpiredClaims(ctx context.Context, before time.Time) (int64, error)
	// LatestPlanOrder returns nil when the user has no completed plan order for the group.
	LatestPlanOrder(ctx context.Context, userID, groupID int64) (*OtohaPlanOrder, error)
}

// PaymentFulfillment describes a completed order for whoever reacts to payments.
type PaymentFulfillment struct {
	OrderID             int64
	UserID              int64
	OrderType           string
	SubscriptionGroupID *int64
}

// PaymentFulfillmentHook runs after an order is fulfilled. It must not fail the payment: errors are its own.
type PaymentFulfillmentHook interface {
	OnPaymentFulfilled(ctx context.Context, done PaymentFulfillment)
}

type otohaKeyIssuer interface {
	GetBindableGroupForUser(ctx context.Context, userID, groupID int64) (*Group, error)
	GenerateKey() (string, error)
	InvalidateAuthCacheByKey(ctx context.Context, key string)
}

type otohaAPIKeyReader interface {
	GetByID(ctx context.Context, id int64) (*APIKey, error)
}

type otohaUserReader interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type otohaGroupReader interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
}

type otohaSubscriptionReader interface {
	GetActiveSubscription(ctx context.Context, userID, groupID int64) (*UserSubscription, error)
}

// OtohaService issues the Otoha key, claim codes and the app configuration.
type OtohaService struct {
	cfg     config.OtohaConfig
	repo    OtohaRepository
	keys    otohaAPIKeyReader
	issuer  otohaKeyIssuer
	users   otohaUserReader
	groups  otohaGroupReader
	subs    otohaSubscriptionReader
	catalog OtohaCatalogReader
	now     func() time.Time
}

// NewOtohaService builds the service from the server configuration's otoha section.
func NewOtohaService(
	cfg *config.Config,
	repo OtohaRepository,
	apiKeyRepo APIKeyRepository,
	apiKeyService *APIKeyService,
	userRepo UserRepository,
	groupRepo GroupRepository,
	subscriptionService *SubscriptionService,
	catalog OtohaCatalogReader,
) *OtohaService {
	var otohaCfg config.OtohaConfig
	if cfg != nil {
		otohaCfg = cfg.Otoha
	}
	return newOtohaServiceWithDeps(otohaCfg, repo, apiKeyRepo, apiKeyService, userRepo, groupRepo, subscriptionService, catalog, time.Now)
}

func newOtohaServiceWithDeps(
	cfg config.OtohaConfig,
	repo OtohaRepository,
	keys otohaAPIKeyReader,
	issuer otohaKeyIssuer,
	users otohaUserReader,
	groups otohaGroupReader,
	subs otohaSubscriptionReader,
	catalog OtohaCatalogReader,
	now func() time.Time,
) *OtohaService {
	if now == nil {
		now = time.Now
	}
	return &OtohaService{cfg: cfg, repo: repo, keys: keys, issuer: issuer, users: users, groups: groups, subs: subs, catalog: catalog, now: now}
}

// Enabled reports whether this server sells Otoha.
func (s *OtohaService) Enabled() bool {
	return s != nil && s.cfg.Enabled()
}

// EnsureDesktopKey returns the user's "Otoha Desktop" key in the Otoha group, creating it when missing. Only
// users who may use the group (an active plan, or the group takes balance) get one.
func (s *OtohaService) EnsureDesktopKey(ctx context.Context, userID int64) (*APIKey, error) {
	if !s.Enabled() {
		return nil, ErrOtohaNotConfigured
	}
	group, err := s.groups.GetByID(ctx, s.cfg.GroupID)
	switch {
	case errors.Is(err, ErrGroupNotFound):
		slog.Error("otoha group not found; check otoha.group_id", "group_id", s.cfg.GroupID)
		return nil, ErrOtohaNotConfigured
	case err != nil:
		return nil, fmt.Errorf("load otoha group: %w", err)
	case !group.IsActive():
		slog.Error("otoha group is not active", "group_id", s.cfg.GroupID, "status", group.Status)
		return nil, ErrOtohaNotConfigured
	}
	if _, err := s.issuer.GetBindableGroupForUser(ctx, userID, s.cfg.GroupID); err != nil {
		if errors.Is(err, ErrGroupNotAllowed) {
			return nil, ErrOtohaNoAccess
		}
		return nil, fmt.Errorf("check otoha group access: %w", err)
	}
	candidate, err := s.issuer.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate otoha key: %w", err)
	}
	key, created, err := s.repo.EnsureNamedAPIKey(ctx, userID, s.cfg.GroupID, OtohaDesktopKeyName, candidate)
	if err != nil {
		return nil, fmt.Errorf("ensure otoha key: %w", err)
	}
	if created {
		s.issuer.InvalidateAuthCacheByKey(ctx, key.Key)
		slog.Info("otoha desktop key created", "user_id", userID, "group_id", s.cfg.GroupID, "api_key_id", key.ID)
	}
	return key, nil
}

// OnPaymentFulfilled makes sure a user who paid for an Otoha plan, or topped up, has the Otoha key. A failure is
// logged and left for the account page, which ensures the key again before issuing a code.
func (s *OtohaService) OnPaymentFulfilled(ctx context.Context, done PaymentFulfillment) {
	if !s.Enabled() {
		return
	}
	switch done.OrderType {
	case payment.OrderTypeSubscription:
		if done.SubscriptionGroupID == nil || *done.SubscriptionGroupID != s.cfg.GroupID {
			return
		}
	case payment.OrderTypeBalance:
	default:
		return
	}
	if _, err := s.EnsureDesktopKey(ctx, done.UserID); err != nil {
		if errors.Is(err, ErrOtohaNoAccess) {
			// A top-up by someone who cannot use the Otoha group: nothing to do.
			slog.Debug("otoha desktop key not needed after payment", "order_id", done.OrderID, "user_id", done.UserID)
			return
		}
		slog.Warn("otoha desktop key not ensured after payment",
			"order_id", done.OrderID, "user_id", done.UserID, "order_type", done.OrderType, "error", err)
	}
}

// OtohaClaimCode is a fresh claim code as the purchase pages show it.
type OtohaClaimCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	OpenURL   string    `json:"open_url"`
}

// CreateClaim issues a one-time code, valid for 15 minutes, that the app exchanges for the user's configuration.
func (s *OtohaService) CreateClaim(ctx context.Context, userID int64) (*OtohaClaimCode, error) {
	if !s.Enabled() {
		return nil, ErrOtohaNotConfigured
	}
	key, err := s.EnsureDesktopKey(ctx, userID)
	if err != nil {
		return nil, err
	}
	if key.Status != StatusActive {
		return nil, ErrOtohaKeyDisabled
	}
	now := s.now()
	if _, err := s.repo.DeleteExpiredClaims(ctx, now.Add(-otohaClaimRetention)); err != nil {
		slog.Warn("otoha expired claims not cleared", "error", err)
	}
	normalized, err := newOtohaClaimCode()
	if err != nil {
		return nil, fmt.Errorf("generate otoha claim code: %w", err)
	}
	claim := &OtohaClaim{
		CodeHash:  hashOtohaClaimCode(normalized),
		UserID:    userID,
		APIKeyID:  key.ID,
		ExpiresAt: now.Add(otohaClaimTTL),
	}
	if err := s.repo.CreateClaim(ctx, claim); err != nil {
		return nil, fmt.Errorf("store otoha claim: %w", err)
	}
	code := formatOtohaClaimCode(normalized)
	return &OtohaClaimCode{Code: code, ExpiresAt: claim.ExpiresAt, OpenURL: otohaOpenURLPrefix + url.QueryEscape(code)}, nil
}

// RedeemClaim exchanges a code for the configuration once. Every failure is ErrOtohaClaimInvalid.
func (s *OtohaService) RedeemClaim(ctx context.Context, code string) (*OtohaConfigFile, error) {
	if !s.Enabled() {
		return nil, ErrOtohaNotConfigured
	}
	normalized, ok := normalizeOtohaClaimCode(code)
	if !ok {
		return nil, ErrOtohaClaimInvalid
	}
	// The code is spent here; a storage failure after this point answers 500 and the user makes a new code.
	claim, err := s.repo.ConsumeClaim(ctx, hashOtohaClaimCode(normalized), s.now())
	if err != nil {
		if errors.Is(err, ErrOtohaClaimNotUsable) {
			return nil, ErrOtohaClaimInvalid
		}
		return nil, fmt.Errorf("consume otoha claim: %w", err)
	}
	key, err := s.keys.GetByID(ctx, claim.APIKeyID)
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			return nil, ErrOtohaClaimInvalid
		}
		return nil, fmt.Errorf("load otoha key: %w", err)
	}
	if key.UserID != claim.UserID || key.Status != StatusActive || key.GroupID == nil || *key.GroupID != s.cfg.GroupID {
		return nil, ErrOtohaClaimInvalid
	}
	user, err := s.users.GetByID(ctx, claim.UserID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrOtohaClaimInvalid
		}
		return nil, fmt.Errorf("load otoha user: %w", err)
	}
	if !user.IsActive() {
		return nil, ErrOtohaClaimInvalid
	}
	return s.buildConfig(ctx, user, key)
}

// ConfigForUser is the configuration file a logged-in user downloads.
func (s *OtohaService) ConfigForUser(ctx context.Context, userID int64) (*OtohaConfigFile, error) {
	if !s.Enabled() {
		return nil, ErrOtohaNotConfigured
	}
	key, err := s.EnsureDesktopKey(ctx, userID)
	if err != nil {
		return nil, err
	}
	if key.Status != StatusActive {
		return nil, ErrOtohaKeyDisabled
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load otoha user: %w", err)
	}
	return s.buildConfig(ctx, user, key)
}

// OtohaConfigFile is the OtohaAI `config.json` (CredentialsFile, version 1).
type OtohaConfigFile struct {
	Version      int                      `json:"version"`
	Providers    []OtohaConfigProvider    `json:"providers"`
	Subscription *OtohaConfigSubscription `json:"subscription,omitempty"`
}

// OtohaConfigProvider is the one compatibleGateway provider.
type OtohaConfigProvider struct {
	Kind    string              `json:"kind"`
	BaseURL string              `json:"baseURL"`
	Model   string              `json:"model,omitempty"`
	APIKey  string              `json:"apiKey"`
	Catalog *OtohaConfigCatalog `json:"catalog,omitempty"`
}

// OtohaConfigCatalog is the group's catalog at the time of issue plus where the app refreshes it.
type OtohaConfigCatalog struct {
	OtohaCatalog
	RefreshURL string `json:"refreshURL,omitempty"`
}

// OtohaConfigSubscription is shown by the app; it grants nothing by itself.
type OtohaConfigSubscription struct {
	Plan      string `json:"plan,omitempty"`
	Account   string `json:"account,omitempty"`
	IssuedAt  string `json:"issuedAt,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// buildConfig runs after a code is spent, so it leaves out what it cannot read rather than failing.
func (s *OtohaService) buildConfig(ctx context.Context, user *User, key *APIKey) (*OtohaConfigFile, error) {
	groupID := s.cfg.GroupID
	group, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		slog.Warn("otoha config: group not read", "group_id", groupID, "error", err)
		group = nil
	}
	catalog := s.readCatalog(ctx, groupID)

	provider := OtohaConfigProvider{
		Kind:    otohaProviderKind,
		BaseURL: s.cfg.GatewayBaseURL,
		Model:   pickOtohaDefaultModel(s.cfg.DefaultModel, catalog, group),
		APIKey:  key.Key,
	}
	if catalog != nil {
		provider.Catalog = &OtohaConfigCatalog{OtohaCatalog: *catalog, RefreshURL: fmt.Sprintf(otohaCatalogRefreshFmt, s.cfg.GatewayBaseURL)}
	}

	subscription := &OtohaConfigSubscription{
		Account:  strings.TrimSpace(user.Email),
		IssuedAt: formatOtohaTime(s.now()),
	}
	sub, err := s.subs.GetActiveSubscription(ctx, user.ID, groupID)
	switch {
	case err == nil && sub != nil:
		order := s.latestPlanOrder(ctx, user.ID)
		subscription.Plan = otohaPlanName(order, group)
		subscription.ExpiresAt = formatOtohaTime(sub.ExpiresAt)
		if order != nil {
			subscription.Reference = order.Reference
		}
	case err != nil && !errors.Is(err, ErrSubscriptionNotFound):
		slog.Warn("otoha config: subscription not read", "user_id", user.ID, "error", err)
	}

	return &OtohaConfigFile{
		Version:      otohaConfigVersion,
		Providers:    []OtohaConfigProvider{provider},
		Subscription: subscription,
	}, nil
}

func (s *OtohaService) readCatalog(ctx context.Context, groupID int64) *OtohaCatalog {
	if s.catalog == nil {
		return nil
	}
	catalog, err := s.catalog.CatalogForGroup(ctx, groupID)
	if err != nil {
		slog.Warn("otoha catalog not read", "group_id", groupID, "error", err)
		return nil
	}
	return catalog
}

func (s *OtohaService) latestPlanOrder(ctx context.Context, userID int64) *OtohaPlanOrder {
	order, err := s.repo.LatestPlanOrder(ctx, userID, s.cfg.GroupID)
	if err != nil {
		slog.Warn("otoha latest plan order not read", "user_id", userID, "error", err)
		return nil
	}
	return order
}

// otohaPlanName is the name of the plan the user last bought, or the group's name.
func otohaPlanName(order *OtohaPlanOrder, group *Group) string {
	if order != nil && strings.TrimSpace(order.PlanName) != "" {
		return strings.TrimSpace(order.PlanName)
	}
	if group != nil {
		return strings.TrimSpace(group.Name)
	}
	return ""
}

// pickOtohaDefaultModel chooses the app's default model: the configured one; else the catalog's model for
// "default" use, its lead model, or its first; else the group's default model or first exact allowlist entry.
func pickOtohaDefaultModel(configured string, catalog *OtohaCatalog, group *Group) string {
	if model := strings.TrimSpace(configured); model != "" {
		return model
	}
	if catalog != nil && len(catalog.Models) > 0 {
		for _, m := range catalog.Models {
			if slices.Contains(m.Use, "default") {
				return m.ID
			}
		}
		for _, m := range catalog.Models {
			if slices.Contains(m.Roles, "lead") {
				return m.ID
			}
		}
		return catalog.Models[0].ID
	}
	if group == nil {
		return ""
	}
	if model := strings.TrimSpace(group.DefaultMappedModel); model != "" {
		return model
	}
	if group.ModelAllowlist.Enabled {
		for _, m := range group.ModelAllowlist.Models {
			if m = strings.TrimSpace(m); m != "" && !strings.Contains(m, "*") {
				return m
			}
		}
	}
	return ""
}

// OtohaAccount is what the Otoha account page shows.
type OtohaAccount struct {
	GroupID int64             `json:"group_id"`
	Email   string            `json:"email"`
	Balance float64           `json:"balance"`
	Plan    *OtohaAccountPlan `json:"plan"`
	Catalog *OtohaCatalog     `json:"catalog"`
}

// OtohaAccountPlan is the active plan and this 30-day period's use.
type OtohaAccountPlan struct {
	// PlanID is the plan the current term was bought with; null for a subscription not bought as a plan.
	PlanID *int64 `json:"plan_id"`
	// HasOwnAllowance: the subscription carries its plan's allowance (a tier), so dearer tiers are upgrades.
	HasOwnAllowance bool       `json:"has_own_allowance"`
	Name            string     `json:"name"`
	ExpiresAt       time.Time  `json:"expires_at"`
	MonthlyLimitUSD *float64   `json:"monthly_limit_usd"`
	MonthlyUsedUSD  float64    `json:"monthly_used_usd"`
	PeriodResetsAt  *time.Time `json:"period_resets_at"`
}

// AccountSummary reads the account page's data without creating anything.
func (s *OtohaService) AccountSummary(ctx context.Context, userID int64) (*OtohaAccount, error) {
	if !s.Enabled() {
		return nil, ErrOtohaNotConfigured
	}
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load otoha user: %w", err)
	}
	group, err := s.groups.GetByID(ctx, s.cfg.GroupID)
	if err != nil {
		return nil, fmt.Errorf("load otoha group: %w", err)
	}
	account := &OtohaAccount{
		GroupID: s.cfg.GroupID,
		Email:   user.Email,
		Balance: user.Balance,
		Catalog: s.readCatalog(ctx, s.cfg.GroupID),
	}
	sub, err := s.subs.GetActiveSubscription(ctx, userID, s.cfg.GroupID)
	if err != nil && !errors.Is(err, ErrSubscriptionNotFound) {
		return nil, fmt.Errorf("load otoha subscription: %w", err)
	}
	if err == nil && sub != nil {
		subs := []UserSubscription{*sub}
		normalizeExpiredWindowsAt(subs, s.now())
		current := subs[0]
		plan := &OtohaAccountPlan{
			PlanID:          current.PlanID,
			HasOwnAllowance: current.hasOwnAllowance(),
			Name:            otohaPlanName(s.latestPlanOrder(ctx, userID), group),
			ExpiresAt:       current.ExpiresAt,
			MonthlyUsedUSD:  current.MonthlyUsageUSD,
			PeriodResetsAt:  current.MonthlyResetTime(),
		}
		// The allowance of the plan the user bought, or the group's limit (TASK-57).
		if limits := current.EffectiveLimits(group); limits.HasMonthly() {
			limit := *limits.MonthlyUSD
			plan.MonthlyLimitUSD = &limit
		}
		account.Plan = plan
	}
	return account, nil
}

func newOtohaClaimCode() (string, error) {
	buf := make([]byte, otohaClaimCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, otohaClaimCodeLength)
	for i, b := range buf {
		out[i] = otohaClaimAlphabet[int(b)%len(otohaClaimAlphabet)] // 256 is a multiple of 32: uniform
	}
	return string(out), nil
}

func formatOtohaClaimCode(normalized string) string {
	var b strings.Builder
	for i := 0; i < len(normalized); i += otohaClaimGroupSize {
		if i > 0 {
			_ = b.WriteByte('-')
		}
		_, _ = b.WriteString(normalized[i:min(i+otohaClaimGroupSize, len(normalized))])
	}
	return b.String()
}

// normalizeOtohaClaimCode accepts a code typed loosely: any case, with or without dashes or spaces, and the
// letters O, I and L read as 0, 1 and 1.
func normalizeOtohaClaimCode(code string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch r {
		case '-', ' ', '\t', '\n', '\r':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		if r > 127 || !strings.ContainsRune(otohaClaimAlphabet, r) {
			return "", false
		}
		_, _ = b.WriteRune(r)
		if b.Len() > otohaClaimCodeLength {
			return "", false
		}
	}
	if b.Len() != otohaClaimCodeLength {
		return "", false
	}
	return b.String(), true
}

func hashOtohaClaimCode(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func formatOtohaTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}
