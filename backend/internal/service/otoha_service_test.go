//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeOtohaRepo struct {
	keys        map[int64]*APIKey
	nextKeyID   int64
	claims      []*OtohaClaim
	planOrder   *OtohaPlanOrder
	ensureCalls int
	ensureErr   error
	purged      int
}

func newFakeOtohaRepo() *fakeOtohaRepo {
	return &fakeOtohaRepo{keys: map[int64]*APIKey{}, nextKeyID: 100}
}

func (r *fakeOtohaRepo) EnsureNamedAPIKey(_ context.Context, userID, groupID int64, name, candidateKey string) (*APIKey, bool, error) {
	r.ensureCalls++
	if r.ensureErr != nil {
		return nil, false, r.ensureErr
	}
	for _, k := range r.keys {
		if k.UserID == userID && k.GroupID != nil && *k.GroupID == groupID && k.Name == name {
			cp := *k
			return &cp, false, nil
		}
	}
	r.nextKeyID++
	gid := groupID
	k := &APIKey{ID: r.nextKeyID, UserID: userID, GroupID: &gid, Name: name, Key: candidateKey, Status: StatusActive}
	r.keys[k.ID] = k
	cp := *k
	return &cp, true, nil
}

func (r *fakeOtohaRepo) CreateClaim(_ context.Context, claim *OtohaClaim) error {
	cp := *claim
	cp.ID = int64(len(r.claims) + 1)
	r.claims = append(r.claims, &cp)
	return nil
}

func (r *fakeOtohaRepo) ConsumeClaim(_ context.Context, codeHash string, now time.Time) (*OtohaClaim, error) {
	for _, c := range r.claims {
		if c.CodeHash == codeHash && c.UsedAt == nil && c.ExpiresAt.After(now) {
			used := now
			c.UsedAt = &used
			cp := *c
			return &cp, nil
		}
	}
	return nil, ErrOtohaClaimNotUsable
}

func (r *fakeOtohaRepo) DeleteExpiredClaims(_ context.Context, _ time.Time) (int64, error) {
	r.purged++
	return 0, nil
}

func (r *fakeOtohaRepo) LatestPlanOrder(_ context.Context, _, _ int64) (*OtohaPlanOrder, error) {
	return r.planOrder, nil
}

func (r *fakeOtohaRepo) GetByID(_ context.Context, id int64) (*APIKey, error) {
	k, ok := r.keys[id]
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	cp := *k
	return &cp, nil
}

type fakeOtohaKeyIssuer struct {
	allowed     bool
	group       *Group
	bindErr     error
	generated   int
	invalidated []string
}

func (f *fakeOtohaKeyIssuer) GetBindableGroupForUser(_ context.Context, _, groupID int64) (*Group, error) {
	if f.bindErr != nil {
		return nil, f.bindErr
	}
	if !f.allowed {
		return nil, ErrGroupNotAllowed
	}
	return f.group, nil
}

func (f *fakeOtohaKeyIssuer) GenerateKey() (string, error) {
	f.generated++
	return "sk-otoha-" + strings.Repeat("x", f.generated), nil
}

func (f *fakeOtohaKeyIssuer) InvalidateAuthCacheByKey(_ context.Context, key string) {
	f.invalidated = append(f.invalidated, key)
}

type fakeOtohaUsers map[int64]*User

func (f fakeOtohaUsers) GetByID(_ context.Context, id int64) (*User, error) {
	u, ok := f[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := *u
	return &cp, nil
}

type fakeOtohaGroups map[int64]*Group

func (f fakeOtohaGroups) GetByID(_ context.Context, id int64) (*Group, error) {
	g, ok := f[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	cp := *g
	return &cp, nil
}

type fakeOtohaSubs struct {
	sub *UserSubscription
	err error
}

func (f *fakeOtohaSubs) GetActiveSubscription(_ context.Context, _, _ int64) (*UserSubscription, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	cp := *f.sub
	return &cp, nil
}

type fakeOtohaCatalog struct {
	catalog *OtohaCatalog
	err     error
}

func (f *fakeOtohaCatalog) CatalogForGroup(_ context.Context, _ int64) (*OtohaCatalog, error) {
	return f.catalog, f.err
}

// --- fixture ---

const otohaTestGroupID int64 = 7

type otohaFixture struct {
	svc     *OtohaService
	repo    *fakeOtohaRepo
	issuer  *fakeOtohaKeyIssuer
	users   fakeOtohaUsers
	groups  fakeOtohaGroups
	subs    *fakeOtohaSubs
	catalog *fakeOtohaCatalog
	now     time.Time
}

func newOtohaFixture(t *testing.T) *otohaFixture {
	t.Helper()
	monthly := 50.0
	group := &Group{ID: otohaTestGroupID, Name: "Otoha", Status: StatusActive, SubscriptionType: SubscriptionTypeSubscription,
		BalanceFallbackEnabled: true, MonthlyLimitUSD: &monthly, DefaultMappedModel: "gpt-6-luna"}
	f := &otohaFixture{
		repo:    newFakeOtohaRepo(),
		issuer:  &fakeOtohaKeyIssuer{allowed: true, group: group},
		users:   fakeOtohaUsers{1: {ID: 1, Email: "someone@example.com", Status: StatusActive, Balance: 12.5}},
		groups:  fakeOtohaGroups{otohaTestGroupID: group},
		subs:    &fakeOtohaSubs{},
		catalog: &fakeOtohaCatalog{},
		now:     time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC),
	}
	f.svc = newOtohaServiceWithDeps(
		config.OtohaConfig{GroupID: otohaTestGroupID, GatewayBaseURL: "https://api.otohaai.com"},
		f.repo, f.repo, f.issuer, f.users, f.groups, f.subs, f.catalog,
		func() time.Time { return f.now },
	)
	return f
}

func requireOtohaReason(t *testing.T, err error, reason string) {
	t.Helper()
	require.Error(t, err)
	require.Equal(t, reason, infraerrors.Reason(err), "unexpected error: %v", err)
}

// --- EnsureDesktopKey ---

func TestOtohaEnsureDesktopKeyCreatesOneKeyInTheOtohaGroup(t *testing.T) {
	f := newOtohaFixture(t)

	key, err := f.svc.EnsureDesktopKey(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, OtohaDesktopKeyName, key.Name)
	require.NotNil(t, key.GroupID)
	require.Equal(t, otohaTestGroupID, *key.GroupID)
	require.Equal(t, []string{key.Key}, f.issuer.invalidated, "a new key clears any cached refusal for it")

	again, err := f.svc.EnsureDesktopKey(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, key.ID, again.ID, "a second payment keeps the same key")
	require.Equal(t, key.Key, again.Key)
	require.Len(t, f.repo.keys, 1)
	require.Len(t, f.issuer.invalidated, 1)
}

func TestOtohaEnsureDesktopKeyRefusesUsersWhoCannotUseTheGroup(t *testing.T) {
	f := newOtohaFixture(t)
	f.issuer.allowed = false

	_, err := f.svc.EnsureDesktopKey(context.Background(), 1)
	requireOtohaReason(t, err, "OTOHA_NO_ACCESS")
	require.Empty(t, f.repo.keys)
}

func TestOtohaEnsureDesktopKeyPassesStorageErrorsOn(t *testing.T) {
	f := newOtohaFixture(t)
	f.repo.ensureErr = errors.New("db down")

	_, err := f.svc.EnsureDesktopKey(context.Background(), 1)
	require.Error(t, err)
	require.NotEqual(t, "OTOHA_NO_ACCESS", infraerrors.Reason(err))
}

func TestOtohaEndpointsAreOffWithoutAGroup(t *testing.T) {
	f := newOtohaFixture(t)
	f.svc = newOtohaServiceWithDeps(config.OtohaConfig{}, f.repo, f.repo, f.issuer, f.users, f.groups, f.subs, f.catalog, time.Now)

	_, err := f.svc.EnsureDesktopKey(context.Background(), 1)
	requireOtohaReason(t, err, "OTOHA_NOT_CONFIGURED")
	_, err = f.svc.CreateClaim(context.Background(), 1)
	requireOtohaReason(t, err, "OTOHA_NOT_CONFIGURED")
	_, err = f.svc.RedeemClaim(context.Background(), "ABCDE-ABCDE-ABCDE-ABCDE")
	requireOtohaReason(t, err, "OTOHA_NOT_CONFIGURED")
	_, err = f.svc.ConfigForUser(context.Background(), 1)
	requireOtohaReason(t, err, "OTOHA_NOT_CONFIGURED")
	_, err = f.svc.AccountSummary(context.Background(), 1)
	requireOtohaReason(t, err, "OTOHA_NOT_CONFIGURED")
}

// --- payment fulfillment ---

func TestOtohaPaymentFulfilledEnsuresKeyForOtohaPlansAndTopUps(t *testing.T) {
	otherGroup := int64(99)
	otoha := otohaTestGroupID
	cases := []struct {
		name      string
		orderType string
		groupID   *int64
		wantKey   bool
	}{
		{name: "Otoha plan", orderType: "subscription", groupID: &otoha, wantKey: true},
		{name: "other plan", orderType: "subscription", groupID: &otherGroup, wantKey: false},
		{name: "plan without group", orderType: "subscription", groupID: nil, wantKey: false},
		{name: "top-up", orderType: "balance", groupID: nil, wantKey: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOtohaFixture(t)
			f.svc.OnPaymentFulfilled(context.Background(), PaymentFulfillment{OrderID: 5, UserID: 1, OrderType: tc.orderType, SubscriptionGroupID: tc.groupID})
			require.Equal(t, tc.wantKey, len(f.repo.keys) == 1)
		})
	}
}

func TestOtohaPaymentFulfilledNeverFailsThePayment(t *testing.T) {
	f := newOtohaFixture(t)
	f.repo.ensureErr = errors.New("db down")
	require.NotPanics(t, func() {
		f.svc.OnPaymentFulfilled(context.Background(), PaymentFulfillment{OrderID: 5, UserID: 1, OrderType: "balance"})
	})

	off := newOtohaServiceWithDeps(config.OtohaConfig{}, f.repo, f.repo, f.issuer, f.users, f.groups, f.subs, f.catalog, time.Now)
	f.repo.ensureCalls = 0
	off.OnPaymentFulfilled(context.Background(), PaymentFulfillment{OrderID: 5, UserID: 1, OrderType: "balance"})
	require.Zero(t, f.repo.ensureCalls)

	var nilSvc *OtohaService
	require.NotPanics(t, func() {
		nilSvc.OnPaymentFulfilled(context.Background(), PaymentFulfillment{OrderID: 5, UserID: 1, OrderType: "balance"})
	})
}

// --- claim codes ---

var otohaCodePattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{5}(-[0-9A-HJKMNP-TV-Z]{5}){3}$`)

func TestOtohaCreateClaimStoresOnlyAHashForFifteenMinutes(t *testing.T) {
	f := newOtohaFixture(t)

	claim, err := f.svc.CreateClaim(context.Background(), 1)
	require.NoError(t, err)
	require.Regexp(t, otohaCodePattern, claim.Code)
	require.Equal(t, f.now.Add(15*time.Minute), claim.ExpiresAt)
	require.Equal(t, "otoha://claim?code="+claim.Code, claim.OpenURL)

	require.Len(t, f.repo.claims, 1)
	stored := f.repo.claims[0]
	sum := sha256.Sum256([]byte(strings.ReplaceAll(claim.Code, "-", "")))
	require.Equal(t, hex.EncodeToString(sum[:]), stored.CodeHash)
	require.NotContains(t, stored.CodeHash, strings.ReplaceAll(claim.Code, "-", ""))
	require.Equal(t, int64(1), stored.UserID)
	require.Len(t, f.repo.keys, 1, "creating a code makes sure the key exists")
	for id := range f.repo.keys {
		require.Equal(t, id, stored.APIKeyID)
	}
	require.Equal(t, 1, f.repo.purged, "old codes are cleared out")

	second, err := f.svc.CreateClaim(context.Background(), 1)
	require.NoError(t, err)
	require.NotEqual(t, claim.Code, second.Code)
}

func TestOtohaCreateClaimRefusesATurnedOffKey(t *testing.T) {
	f := newOtohaFixture(t)
	key, err := f.svc.EnsureDesktopKey(context.Background(), 1)
	require.NoError(t, err)
	f.repo.keys[key.ID].Status = StatusDisabled

	_, err = f.svc.CreateClaim(context.Background(), 1)
	requireOtohaReason(t, err, "OTOHA_KEY_DISABLED")
	require.Empty(t, f.repo.claims)
}

func TestOtohaRedeemClaimReturnsTheAppConfiguration(t *testing.T) {
	f := newOtohaFixture(t)
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		ExpiresAt: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)}
	f.repo.planOrder = &OtohaPlanOrder{PlanName: "Otoha Pro", Reference: "sub2_20261004ABC"}
	f.catalog.catalog = &OtohaCatalog{Schema: 1, Revision: "r7", Models: []OtohaCatalogModel{
		{ID: "deepseek-v4", Name: "DeepSeek V4", Inputs: []string{"text"}, Tools: true, Cost: "low",
			Price: OtohaModelPrice{Currency: "USD", Per: "1M tokens", Input: 0.5, Output: 1}, Use: []string{"fast"}},
		{ID: "gpt-6-luna", Name: "GPT-6 Luna", Inputs: []string{"text", "image"}, Images: true, Tools: true, Cost: "standard",
			Price: OtohaModelPrice{Currency: "USD", Per: "1M tokens", Input: 2, Output: 8}, Use: []string{"default", "writing"}},
	}}

	claim, err := f.svc.CreateClaim(context.Background(), 1)
	require.NoError(t, err)
	f.now = f.now.Add(5 * time.Minute)

	cfg, err := f.svc.RedeemClaim(context.Background(), claim.Code)
	require.NoError(t, err)
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var key *APIKey
	for _, k := range f.repo.keys {
		key = k
	}
	want := `{
	  "version": 1,
	  "providers": [{
	    "kind": "compatibleGateway",
	    "baseURL": "https://api.otohaai.com",
	    "model": "gpt-6-luna",
	    "apiKey": "` + key.Key + `",
	    "catalog": {
	      "schema": 1,
	      "revision": "r7",
	      "refreshURL": "https://api.otohaai.com/v1/models?client=otoha",
	      "models": [
	        {"id": "deepseek-v4", "name": "DeepSeek V4", "inputs": ["text"], "images": false, "tools": true, "cost": "low",
	         "price": {"currency": "USD", "per": "1M tokens", "input": 0.5, "output": 1}, "use": ["fast"]},
	        {"id": "gpt-6-luna", "name": "GPT-6 Luna", "inputs": ["text", "image"], "images": true, "tools": true, "cost": "standard",
	         "price": {"currency": "USD", "per": "1M tokens", "input": 2, "output": 8}, "use": ["default", "writing"]}
	      ]
	    }
	  }],
	  "subscription": {
	    "plan": "Otoha Pro",
	    "account": "someone@example.com",
	    "issuedAt": "2026-10-04T08:05:00Z",
	    "expiresAt": "2026-11-03T08:00:00Z",
	    "reference": "sub2_20261004ABC"
	  }
	}`
	require.JSONEq(t, want, string(raw))
}

func TestOtohaRedeemClaimWorksOnceAndFailsTheSameWayForEveryBadCode(t *testing.T) {
	f := newOtohaFixture(t)
	used, err := f.svc.CreateClaim(context.Background(), 1)
	require.NoError(t, err)
	_, err = f.svc.RedeemClaim(context.Background(), used.Code)
	require.NoError(t, err)

	expired, err := f.svc.CreateClaim(context.Background(), 1)
	require.NoError(t, err)

	f.now = f.now.Add(15*time.Minute + time.Second)
	var failures []error
	for _, code := range []string{used.Code, expired.Code, "ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ", "not a code", ""} {
		_, err := f.svc.RedeemClaim(context.Background(), code)
		require.Error(t, err, "code %q", code)
		failures = append(failures, err)
	}
	first := failures[0]
	for _, err := range failures {
		require.Equal(t, infraerrors.Code(first), infraerrors.Code(err))
		require.Equal(t, "OTOHA_CLAIM_INVALID", infraerrors.Reason(err))
		require.Equal(t, infraerrors.Message(first), infraerrors.Message(err))
	}
}

func TestOtohaRedeemClaimAcceptsLooselyTypedCodes(t *testing.T) {
	f := newOtohaFixture(t)
	claim, err := f.svc.CreateClaim(context.Background(), 1)
	require.NoError(t, err)

	typed := " " + strings.ToLower(strings.ReplaceAll(claim.Code, "-", " ")) + " "
	_, err = f.svc.RedeemClaim(context.Background(), typed)
	require.NoError(t, err)
}

func TestOtohaRedeemClaimRefusesWhenKeyOrAccountChanged(t *testing.T) {
	cases := []struct {
		name   string
		change func(f *otohaFixture, keyID int64)
	}{
		{name: "key deleted", change: func(f *otohaFixture, keyID int64) { delete(f.repo.keys, keyID) }},
		{name: "key turned off", change: func(f *otohaFixture, keyID int64) { f.repo.keys[keyID].Status = StatusDisabled }},
		{name: "key moved to another group", change: func(f *otohaFixture, keyID int64) {
			other := int64(99)
			f.repo.keys[keyID].GroupID = &other
		}},
		{name: "account disabled", change: func(f *otohaFixture, _ int64) { f.users[1].Status = StatusDisabled }},
		{name: "account gone", change: func(f *otohaFixture, _ int64) { delete(f.users, 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOtohaFixture(t)
			claim, err := f.svc.CreateClaim(context.Background(), 1)
			require.NoError(t, err)
			tc.change(f, f.repo.claims[0].APIKeyID)

			_, err = f.svc.RedeemClaim(context.Background(), claim.Code)
			requireOtohaReason(t, err, "OTOHA_CLAIM_INVALID")
		})
	}
}

func TestOtohaConfigOmitsWhatIsNotKnown(t *testing.T) {
	f := newOtohaFixture(t)
	f.catalog.err = errors.New("catalog unavailable")

	cfg, err := f.svc.ConfigForUser(context.Background(), 1)
	require.NoError(t, err)
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	provider := doc["providers"].([]any)[0].(map[string]any)
	require.NotContains(t, provider, "catalog")
	require.Equal(t, "gpt-6-luna", provider["model"], "the group's default model when there is no catalog")
	sub := doc["subscription"].(map[string]any)
	require.Equal(t, "someone@example.com", sub["account"])
	require.NotContains(t, sub, "plan", "no plan without an active subscription")
	require.NotContains(t, sub, "expiresAt")
	require.Len(t, f.repo.keys, 1, "downloading the configuration makes sure the key exists")
}

func TestOtohaConfigNamesThePlanAfterTheGroupWhenTheOrderIsUnknown(t *testing.T) {
	f := newOtohaFixture(t)
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		ExpiresAt: f.now.Add(10 * 24 * time.Hour)}

	cfg, err := f.svc.ConfigForUser(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, cfg.Subscription)
	require.Equal(t, "Otoha", cfg.Subscription.Plan)
	require.Empty(t, cfg.Subscription.Reference)
}

func TestPickOtohaDefaultModel(t *testing.T) {
	catalog := func(models ...OtohaCatalogModel) *OtohaCatalog { return &OtohaCatalog{Models: models} }
	cases := []struct {
		name       string
		configured string
		catalog    *OtohaCatalog
		group      *Group
		want       string
	}{
		{name: "configured wins", configured: "claude-opus-5-5", catalog: catalog(OtohaCatalogModel{ID: "a", Use: []string{"default"}}), want: "claude-opus-5-5"},
		{name: "catalog default", catalog: catalog(OtohaCatalogModel{ID: "a"}, OtohaCatalogModel{ID: "b", Use: []string{"fast", "default"}}), want: "b"},
		{name: "catalog lead", catalog: catalog(OtohaCatalogModel{ID: "a"}, OtohaCatalogModel{ID: "b", Roles: []string{"lead"}}), want: "b"},
		{name: "catalog first", catalog: catalog(OtohaCatalogModel{ID: "a"}, OtohaCatalogModel{ID: "b"}), group: &Group{DefaultMappedModel: "g"}, want: "a"},
		{name: "group default", catalog: catalog(), group: &Group{DefaultMappedModel: "g"}, want: "g"},
		{name: "allowlist first exact name", group: &Group{ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"gpt-*", "grok-5"}}}, want: "grok-5"},
		{name: "allowlist off", group: &Group{ModelAllowlist: GroupModelAllowlist{Enabled: false, Models: []string{"grok-5"}}}, want: ""},
		{name: "nothing", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, pickOtohaDefaultModel(tc.configured, tc.catalog, tc.group))
		})
	}
}

// --- account page ---

func TestOtohaAccountSummaryShowsPlanQuotaBalanceAndCatalog(t *testing.T) {
	f := newOtohaFixture(t)
	windowStart := f.now.Add(-3 * 24 * time.Hour)
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		StartsAt: windowStart, ExpiresAt: f.now.Add(27 * 24 * time.Hour), MonthlyWindowStart: &windowStart, MonthlyUsageUSD: 12.25}
	f.repo.planOrder = &OtohaPlanOrder{PlanName: "Otoha Pro", Reference: "sub2_1"}
	f.catalog.catalog = &OtohaCatalog{Schema: 1, Revision: "r1", Models: []OtohaCatalogModel{{ID: "gpt-6-luna", Name: "GPT-6 Luna"}}}

	summary, err := f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, otohaTestGroupID, summary.GroupID)
	require.Equal(t, "someone@example.com", summary.Email)
	require.Equal(t, 12.5, summary.Balance)
	require.NotNil(t, summary.Plan)
	require.Equal(t, "Otoha Pro", summary.Plan.Name)
	require.Equal(t, f.subs.sub.ExpiresAt, summary.Plan.ExpiresAt)
	require.NotNil(t, summary.Plan.MonthlyLimitUSD)
	require.Equal(t, 50.0, *summary.Plan.MonthlyLimitUSD)
	require.Equal(t, 12.25, summary.Plan.MonthlyUsedUSD)
	require.NotNil(t, summary.Plan.PeriodResetsAt)
	require.Equal(t, windowStart.Add(30*24*time.Hour), *summary.Plan.PeriodResetsAt)
	require.NotNil(t, summary.Catalog)
	require.Equal(t, "r1", summary.Catalog.Revision)
	require.Empty(t, f.repo.keys, "looking at the account does not create a key")
}

func TestOtohaAccountSummaryWithoutPlanOrWithAFinishedPeriod(t *testing.T) {
	f := newOtohaFixture(t)
	summary, err := f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.Nil(t, summary.Plan)
	require.Nil(t, summary.Catalog)

	oldStart := f.now.Add(-40 * 24 * time.Hour)
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		StartsAt: oldStart, ExpiresAt: f.now.Add(20 * 24 * time.Hour), MonthlyWindowStart: &oldStart, MonthlyUsageUSD: 49}
	summary, err = f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, summary.Plan)
	require.Zero(t, summary.Plan.MonthlyUsedUSD, "a finished 30-day period counts as unused")
	require.Nil(t, summary.Plan.PeriodResetsAt)
}

func TestOtohaAccountSummaryPassesSubscriptionErrorsOn(t *testing.T) {
	f := newOtohaFixture(t)
	f.subs.err = errors.New("db down")
	_, err := f.svc.AccountSummary(context.Background(), 1)
	require.Error(t, err)
}
