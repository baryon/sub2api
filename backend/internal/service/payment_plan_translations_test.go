//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// TASK-82: a plan carries its name, description and features per language. A language, or a field of it, the
// admin has not filled falls back to the plan's own text, so nothing is ever shown empty.

func translatedPlan() *dbent.SubscriptionPlan {
	return &dbent.SubscriptionPlan{
		Name: "标准版", Description: "适合日常使用", Features: "每月额度\n全部模型",
		Translations: map[string]map[string]string{
			"en": {"name": "Standard", "description": "For everyday use", "features": "Monthly allowance\nAll models"},
			"ja": {"name": "スタンダード"},
		},
	}
}

func TestPlanLocalesFollowTheRequestsPreference(t *testing.T) {
	require.Equal(t, []string{"en-us", "en", "ja"}, PlanLocales("en-US,en;q=0.9,ja;q=0.8"))
	require.Equal(t, []string{"ja", "en"}, PlanLocales("en;q=0.5, ja"), "a higher weight comes first")
	require.Equal(t, []string{"zh-cn", "zh"}, PlanLocales("zh-CN"))
	require.Equal(t, []string{"en"}, PlanLocales("en;q=0.9, de;q=0, *;q=0.1"), "q=0 and * are not a preference")
	require.Empty(t, PlanLocales(""))
	require.Empty(t, PlanLocales(" , ;q=x"))
}

func TestLocalizedPlanTextUsesTheRequestsLanguage(t *testing.T) {
	got := LocalizedPlanText(translatedPlan(), "en")
	require.Equal(t, PlanText{Name: "Standard", Description: "For everyday use", Features: "Monthly allowance\nAll models"}, got)

	got = LocalizedPlanText(translatedPlan(), "en-GB,en;q=0.8")
	require.Equal(t, "Standard", got.Name, "a regional variant reads the language's text")
}

func TestLocalizedPlanTextFallsBackToThePlansOwnText(t *testing.T) {
	original := PlanText{Name: "标准版", Description: "适合日常使用", Features: "每月额度\n全部模型"}
	require.Equal(t, original, LocalizedPlanText(translatedPlan(), ""), "no language in the request")
	require.Equal(t, original, LocalizedPlanText(translatedPlan(), "zh-CN"), "the plan's own text is Chinese")
	require.Equal(t, original, LocalizedPlanText(translatedPlan(), "fr"), "a language nobody filled")

	got := LocalizedPlanText(translatedPlan(), "ja")
	require.Equal(t, "スタンダード", got.Name)
	require.Equal(t, "适合日常使用", got.Description, "an unfilled field of a filled language falls back on its own")
	require.Equal(t, "每月额度\n全部模型", got.Features)

	require.Equal(t, PlanText{Name: "x"}, LocalizedPlanText(&dbent.SubscriptionPlan{Name: "x"}, "en"), "a plan without any translation")
	require.Equal(t, PlanText{}, LocalizedPlanText(nil, "en"))
}

func TestLocalizedPlanTextKeepsTheOriginalForAChineseReader(t *testing.T) {
	got := LocalizedPlanText(translatedPlan(), "zh-CN,zh;q=0.9,en;q=0.8")
	require.Equal(t, "标准版", got.Name, "a browser that lists English after Chinese still gets the plan's Chinese text")
}

func TestLocalizedPlanTextTriesTheNextPreferredLanguage(t *testing.T) {
	got := LocalizedPlanText(translatedPlan(), "fr,ja;q=0.8,en;q=0.5")
	require.Equal(t, "スタンダード", got.Name, "ja is preferred over en; fr has nothing")
	require.Equal(t, "For everyday use", LocalizedPlanText(translatedPlan(), "fr,en;q=0.5").Description)
}

func TestNormalizePlanTranslations(t *testing.T) {
	got, err := NormalizePlanTranslations(map[string]map[string]string{
		" EN ":  {"name": "  Standard ", "description": "", "features": "A\nB"},
		"ja":    {"name": " "},
		"zh_TW": {"name": "標準版"},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]string{
		"en":    {"name": "Standard", "features": "A\nB"},
		"zh-tw": {"name": "標準版"},
	}, got, "locales are lower-cased, text trimmed, empty fields and languages dropped")

	got, err = NormalizePlanTranslations(nil)
	require.NoError(t, err)
	require.NotNil(t, got, "stored as {} and never null")
	require.Empty(t, got)
}

func TestNormalizePlanTranslationsRejectsWhatCannotBeShown(t *testing.T) {
	bad := []map[string]map[string]string{
		{"english!": {"name": "x"}},
		{"": {"name": "x"}},
		{"en": {"title": "x"}},
		{"en": {"name": strings.Repeat("a", 101)}},
		{"en": {"name": "a"}, "EN": {"name": "b"}},
	}
	for _, in := range bad {
		_, err := NormalizePlanTranslations(in)
		require.Error(t, err, "%v", in)
		require.Equal(t, "PLAN_TRANSLATIONS_INVALID", infraerrors.Reason(err))
	}
}

func TestPlansKeepTheirTranslations(t *testing.T) {
	ctx := context.Background()
	svc := &PaymentConfigService{entClient: newPaymentConfigServiceTestClient(t)}

	req := planAllowanceRequest(7)
	req.Translations = map[string]map[string]string{"EN": {"name": " Pro ", "description": "Everyday"}}
	plan, err := svc.CreatePlan(ctx, req)
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]string{"en": {"name": "Pro", "description": "Everyday"}}, plan.Translations)

	plain, err := svc.CreatePlan(ctx, planAllowanceRequest(7))
	require.NoError(t, err)
	require.Empty(t, plain.Translations, "a plan created as before has none")

	updated, err := svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{Name: strPtrForPlanTest("专业版")})
	require.NoError(t, err)
	require.Equal(t, "Pro", updated.Translations["en"]["name"], "not mentioned: unchanged")

	replaced := map[string]map[string]string{"ja": {"name": "プロ"}}
	updated, err = svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{Translations: &replaced})
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]string{"ja": {"name": "プロ"}}, updated.Translations, "what is sent replaces all languages")

	cleared := map[string]map[string]string{}
	updated, err = svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{Translations: &cleared})
	require.NoError(t, err)
	require.Empty(t, updated.Translations)

	invalid := map[string]map[string]string{"en": {"price": "1"}}
	_, err = svc.UpdatePlan(ctx, plan.ID, UpdatePlanRequest{Translations: &invalid})
	require.Equal(t, "PLAN_TRANSLATIONS_INVALID", infraerrors.Reason(err))
	req.Translations = invalid
	_, err = svc.CreatePlan(ctx, req)
	require.Equal(t, "PLAN_TRANSLATIONS_INVALID", infraerrors.Reason(err))
}

func TestPlanNameFollowsTheLanguageOfTheContext(t *testing.T) {
	ctx := context.Background()
	svc := &PaymentConfigService{entClient: newPaymentConfigServiceTestClient(t)}
	req := planAllowanceRequest(7)
	req.Name = "标准版"
	req.Translations = map[string]map[string]string{"en": {"name": "Standard"}}
	plan, err := svc.CreatePlan(ctx, req)
	require.NoError(t, err)

	name, err := svc.PlanName(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, "标准版", name, "no language: unchanged")
	name, _ = svc.PlanName(WithPlanLocale(ctx, "en-US,en;q=0.9"), plan.ID)
	require.Equal(t, "Standard", name)
	name, _ = svc.PlanName(WithPlanLocale(ctx, "zh-CN"), plan.ID)
	require.Equal(t, "标准版", name)
}

func TestOtohaPlanNameFollowsTheLanguage(t *testing.T) {
	order := &OtohaPlanOrder{PlanName: "标准版", PlanTranslations: map[string]map[string]string{"en": {"name": "Standard"}}}
	group := &Group{Name: "Otoha"}
	require.Equal(t, "标准版", otohaPlanName(order, group, ""))
	require.Equal(t, "Standard", otohaPlanName(order, group, "en"))
	require.Equal(t, "标准版", otohaPlanName(order, group, "ja"))
	require.Equal(t, "Otoha", otohaPlanName(nil, group, "en"), "no plan: the group's name")
}

func TestOtohaAccountSummaryPlanNameFollowsTheLanguage(t *testing.T) {
	f := newOtohaFixture(t)
	start := f.now.Add(-time.Hour)
	f.subs.sub = &UserSubscription{ID: 3, UserID: 1, GroupID: otohaTestGroupID, Status: SubscriptionStatusActive,
		StartsAt: start, ExpiresAt: f.now.Add(24 * time.Hour)}
	f.repo.planOrder = &OtohaPlanOrder{PlanName: "标准版", Reference: "sub2_3", PlanTranslations: map[string]map[string]string{"en": {"name": "Standard"}}}

	summary, err := f.svc.AccountSummary(WithPlanLocale(context.Background(), "en-US,en;q=0.9"), 1)
	require.NoError(t, err)
	require.Equal(t, "Standard", summary.Plan.Name)

	summary, err = f.svc.AccountSummary(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "标准版", summary.Plan.Name, "no language: unchanged")
}
