package service

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// A plan's name, description and features per language (TASK-82). They are stored as
// {"en": {"name": "...", "description": "...", "features": "..."}}; the plan's own fields stay the text a
// language without its own shows. Languages are free-form tags, so adding one needs no code change.

const (
	planTextName        = "name"
	planTextDescription = "description"
	planTextFeatures    = "features"
	planNameMaxRunes    = 100
	// planOwnLanguage is the language the plan's own fields are written in. A buyer who prefers it is never
	// moved on to a later language of the header.
	planOwnLanguage = "zh"
)

var planLocaleTag = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)

// PlanText is the text of a plan a buyer reads.
type PlanText struct {
	Name        string
	Description string
	Features    string
}

type planLocaleKey struct{}

// WithPlanLocale carries the request's Accept-Language to code that reads plan text further down.
func WithPlanLocale(ctx context.Context, acceptLanguage string) context.Context {
	return context.WithValue(ctx, planLocaleKey{}, acceptLanguage)
}

// PlanLocaleFromContext is the Accept-Language WithPlanLocale carried; empty when the request had none.
func PlanLocaleFromContext(ctx context.Context) string {
	v, _ := ctx.Value(planLocaleKey{}).(string)
	return v
}

// PlanLocales lists the languages an Accept-Language header prefers, best first, as lower-case tags: each tag
// and then its base language ("en-US" gives "en-us", "en"). Weight 0 and "*" are not a preference.
func PlanLocales(acceptLanguage string) []string {
	type weighted struct {
		tag string
		q   float64
		pos int
	}
	var prefs []weighted
	for i, part := range strings.Split(acceptLanguage, ",") {
		fields := strings.Split(part, ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, param := range fields[1:] {
			if k, v, ok := strings.Cut(strings.TrimSpace(param), "="); ok && strings.TrimSpace(k) == "q" {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
				if err != nil {
					q = 0
				} else {
					q = parsed
				}
			}
		}
		if q <= 0 {
			continue
		}
		prefs = append(prefs, weighted{tag: strings.ReplaceAll(tag, "_", "-"), q: q, pos: i})
	}
	sort.SliceStable(prefs, func(a, b int) bool { return prefs[a].q > prefs[b].q })

	var out []string
	seen := map[string]bool{}
	add := func(tag string) {
		if !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	for _, p := range prefs {
		add(p.tag)
		if base, _, ok := strings.Cut(p.tag, "-"); ok {
			add(base)
		}
	}
	return out
}

// LocalizedPlanText is the plan's text in the language the request prefers. Each field falls back, on its
// own, to the plan's own text when no preferred language has it.
func LocalizedPlanText(plan *dbent.SubscriptionPlan, acceptLanguage string) PlanText {
	if plan == nil {
		return PlanText{}
	}
	return PlanText{
		Name:        localizedPlanField(plan.Translations, acceptLanguage, planTextName, plan.Name),
		Description: localizedPlanField(plan.Translations, acceptLanguage, planTextDescription, plan.Description),
		Features:    localizedPlanField(plan.Translations, acceptLanguage, planTextFeatures, plan.Features),
	}
}

func localizedPlanField(translations map[string]map[string]string, acceptLanguage, field, fallback string) string {
	if len(translations) == 0 {
		return fallback
	}
	for _, locale := range PlanLocales(acceptLanguage) {
		if text := strings.TrimSpace(translations[locale][field]); text != "" {
			return text
		}
		if locale == planOwnLanguage {
			return fallback
		}
	}
	return fallback
}

// NormalizePlanTranslations checks what the admin entered and returns it in storage form: language tags in
// lower case, text trimmed, empty fields and empty languages dropped. The result is never nil.
func NormalizePlanTranslations(in map[string]map[string]string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string, len(in))
	seen := make(map[string]bool, len(in))
	for rawLocale, fields := range in {
		locale := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(rawLocale)), "_", "-")
		if !planLocaleTag.MatchString(locale) {
			return nil, infraerrors.BadRequest("PLAN_TRANSLATIONS_INVALID", "language must be a tag such as en or ja: "+rawLocale)
		}
		if seen[locale] {
			return nil, infraerrors.BadRequest("PLAN_TRANSLATIONS_INVALID", "language given twice: "+locale)
		}
		seen[locale] = true
		clean := make(map[string]string, len(fields))
		for field, text := range fields {
			if field != planTextName && field != planTextDescription && field != planTextFeatures {
				return nil, infraerrors.BadRequest("PLAN_TRANSLATIONS_INVALID", "unknown translated field: "+field)
			}
			text = strings.TrimSpace(text)
			if field == planTextName && utf8.RuneCountInString(text) > planNameMaxRunes {
				return nil, infraerrors.BadRequest("PLAN_TRANSLATIONS_INVALID", "translated name is too long")
			}
			if text != "" {
				clean[field] = text
			}
		}
		if len(clean) > 0 {
			out[locale] = clean
		}
	}
	return out, nil
}
