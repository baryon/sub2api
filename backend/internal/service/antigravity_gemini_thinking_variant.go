package service

import (
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/tidwall/gjson"
)

// Gemini 原生请求（/v1beta/models/{model}:generateContent 等）及 Claude/Responses 兼容请求经
// Antigravity 账号转发时，客户端常发无后缀主模型名（如 gemini-3.8-flash、gemini-4-neon、
// claude-opus-5-5）并用 thinkingConfig / effort 表达思考深度；而 Antigravity 上游模型目录只登记
// -low / -medium / -high / -xhigh / -tiered* 等带后缀的变体，裸名直接转发会被上游以 404 拒绝。
//
// 当账号 model_mapping 未把裸名显式指向其它具体模型（未配置，或配置为自身如 x -> x）且存在其档位变体时，
// 按请求的思考深度自动挑选对应变体，不存在时按邻近档位降级。

var geminiThinkingVariantSuffixes = []string{
	"-tiered-xhigh",
	"-tiered-high",
	"-tiered",
	"-sonar",
	"-xhigh",
	"-high",
	"-medium",
	"-low",
}

var antigravityClaudeTierBaseModels = []string{
	"claude-opus-5-5",
	"claude-sonnet-5-5",
}

const (
	geminiThinkingBudgetLowMax    = 1024
	geminiThinkingBudgetMediumMax = 8192
)

// splitAntigravityThinkingTierVariant splits an Antigravity tier-suffixed model ID into its
// lowercase public base model and canonical reasoning level ("" for non-level backing variants
// such as -tiered or -sonar).
func splitAntigravityThinkingTierVariant(model string) (baseModel, level string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(model, "models/")))
	for _, claudeBase := range antigravityClaudeTierBaseModels {
		for _, lvl := range []string{"low", "medium", "high"} {
			if lower == claudeBase+"-"+lvl {
				return claudeBase, lvl, true
			}
		}
	}
	if !strings.HasPrefix(lower, "gemini-") {
		return "", "", false
	}
	for _, suffix := range geminiThinkingVariantSuffixes {
		if strings.HasSuffix(lower, suffix) {
			base := lower[:len(lower)-len(suffix)]
			if base == "" || base == "gemini" {
				return "", "", false
			}
			switch suffix {
			case "-low":
				return base, "low", true
			case "-medium":
				return base, "medium", true
			case "-high", "-tiered-high":
				return base, "high", true
			case "-xhigh", "-tiered-xhigh":
				return base, "xhigh", true
			default:
				return base, "", true
			}
		}
	}
	return "", "", false
}

func isAntigravityTieredBaseCandidate(model string) bool {
	lower := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(model, "models/")))
	if strings.HasPrefix(lower, "gemini-") {
		_, _, isVariant := splitAntigravityThinkingTierVariant(lower)
		return !isVariant
	}
	for _, claudeBase := range antigravityClaudeTierBaseModels {
		if lower == claudeBase {
			return true
		}
	}
	return false
}

// geminiThinkingLevelFromBody 从 Gemini 原生请求体推导期望的思考档位（low/medium/high/xhigh）。
// 同时兼容 camelCase 与 snake_case 字段；解析失败或未携带 thinkingConfig 时返回 "high"。
func geminiThinkingLevelFromBody(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return "high"
	}
	cfg := gjson.GetBytes(body, "generationConfig")
	if !cfg.Exists() {
		cfg = gjson.GetBytes(body, "generation_config")
	}
	tc := cfg.Get("thinkingConfig")
	if !tc.Exists() {
		tc = cfg.Get("thinking_config")
	}
	if !tc.Exists() {
		return "high"
	}
	levelField := tc.Get("thinkingLevel")
	if !levelField.Exists() {
		levelField = tc.Get("thinking_level")
	}
	switch strings.ToLower(strings.TrimSpace(levelField.String())) {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	}
	budgetField := tc.Get("thinkingBudget")
	if !budgetField.Exists() {
		budgetField = tc.Get("thinking_budget")
	}
	if !budgetField.Exists() || budgetField.Type != gjson.Number {
		return "high"
	}
	budget := budgetField.Float()
	switch {
	case budget < 0:
		return "high" // -1 = 动态思考
	case budget <= geminiThinkingBudgetLowMax:
		return "low" // 含 0（关闭思考）：上游没有"无思考"变体，取最浅档，budget 本身仍原样透传
	case budget <= geminiThinkingBudgetMediumMax:
		return "medium"
	default:
		return "high"
	}
}

// geminiThinkingLevelFromClaudeBody 结合兼容层转换后的 output_config.effort 与 thinking 配置推导档位。
func geminiThinkingLevelFromClaudeBody(claudeBody []byte, thinking *antigravity.ThinkingConfig) string {
	switch parseClaudeThinkingIntent(claudeBody).tier {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh":
		return "xhigh"
	default:
		return geminiThinkingLevelFromClaudeThinking(thinking)
	}
}

func antigravityThinkingLevelFromClaudeRequest(req *antigravity.ClaudeRequest) string {
	if req == nil {
		return "high"
	}
	if req.Thinking != nil && strings.EqualFold(strings.TrimSpace(req.Thinking.Type), "disabled") {
		return "low"
	}
	if req.OutputConfig != nil {
		switch strings.ToLower(strings.TrimSpace(req.OutputConfig.Effort)) {
		case "none", "minimal", "low":
			return "low"
		case "medium":
			return "medium"
		case "high":
			return "high"
		case "xhigh", "max":
			return "xhigh"
		}
	}
	return geminiThinkingLevelFromClaudeThinking(req.Thinking)
}

// geminiThinkingLevelFromClaudeThinking 用 Claude Messages 协议的 thinking 配置推导档位，
// 阈值与 geminiThinkingLevelFromBody 保持一致。
func geminiThinkingLevelFromClaudeThinking(thinking *antigravity.ThinkingConfig) string {
	if thinking == nil {
		return "high"
	}
	if strings.EqualFold(strings.TrimSpace(thinking.Type), "disabled") {
		return "low"
	}
	budget := thinking.BudgetTokens
	switch {
	case budget <= 0:
		return "high" // 动态思考 / 未指定预算
	case budget <= geminiThinkingBudgetLowMax:
		return "low"
	case budget <= geminiThinkingBudgetMediumMax:
		return "medium"
	default:
		return "high"
	}
}

// resolveGeminiThinkingVariant 为裸模型名挑选账号映射表里存在的思考深度变体。
func resolveGeminiThinkingVariant(account *Account, requestedModel string, body []byte) (string, bool) {
	return resolveGeminiThinkingVariantForLevel(account, requestedModel, geminiThinkingLevelFromBody(body))
}

func antigravityVariantCandidateOrder(preferred string) []string {
	switch preferred {
	case "xhigh":
		return []string{"xhigh", "tiered-xhigh", "high", "tiered-high", "medium", "low", "tiered"}
	case "medium":
		return []string{"medium", "high", "tiered-high", "low", "tiered", "xhigh", "tiered-xhigh"}
	case "low":
		return []string{"low", "medium", "high", "tiered-high", "tiered", "xhigh", "tiered-xhigh"}
	default:
		return []string{"high", "tiered-high", "medium", "low", "tiered", "xhigh", "tiered-xhigh"}
	}
}

// resolveGeminiThinkingVariantForLevel 是协议无关的档位变体解析内核：
// 支持 Gemini 裸模型及 Antigravity Claude 5.5 裸模型自映射，显式指向具体目标的映射始终保持不变。
func resolveGeminiThinkingVariantForLevel(account *Account, requestedModel string, preferred string) (string, bool) {
	if account == nil {
		return "", false
	}
	model := strings.TrimSpace(strings.TrimPrefix(requestedModel, "models/"))
	if !isAntigravityTieredBaseCandidate(model) {
		return "", false
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return "", false
	}
	// 裸名若已显式映射到其它具体模型（如 claude-sonnet-5-5 -> claude-sonnet-5-5-medium），尊重显式目标。
	mapped, modelMatched := resolveRequestedModelInMapping(mapping, model)
	if modelMatched && strings.TrimSpace(mapped) != model {
		return "", false
	}

	base := strings.ToLower(model)
	for _, suffix := range antigravityVariantCandidateOrder(preferred) {
		candidate := base + "-" + suffix
		var (
			variantMapped  string
			variantMatched bool
		)
		if modelMatched {
			variantMapped, variantMatched = mapping[candidate]
		} else {
			variantMapped, variantMatched = resolveRequestedModelInMapping(mapping, candidate)
		}
		if variantMatched && strings.TrimSpace(variantMapped) != "" {
			return variantMapped, true
		}
	}
	return "", false
}

func stripAntigravityTierDisplaySuffix(name string) string {
	trimmed := strings.TrimSpace(name)
	lower := strings.ToLower(trimmed)
	for _, suffix := range []string{
		" (xhigh)", " (high)", " (medium)", " (low)", " (tiered)",
		" xhigh", " high", " medium", " low", " tiered",
	} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimSpace(trimmed[:len(trimmed)-len(suffix)])
		}
	}
	return trimmed
}

func antigravityVariantRank(variantKey string) int {
	lower := strings.ToLower(strings.TrimSpace(variantKey))
	for idx, suffix := range []string{"-high", "-medium", "-low", "-xhigh", "-tiered-high", "-tiered-xhigh", "-tiered", "-sonar"} {
		if strings.HasSuffix(lower, suffix) {
			return idx
		}
	}
	return 100
}

func explicitAntigravityModelMapping(account *Account) map[string]string {
	out := map[string]string{}
	if account == nil || account.Credentials == nil {
		return out
	}
	switch raw := account.Credentials["model_mapping"].(type) {
	case map[string]any:
		for alias, target := range raw {
			if text, ok := target.(string); ok {
				out[alias] = strings.TrimSpace(text)
			}
		}
	case map[string]string:
		for alias, target := range raw {
			out[alias] = strings.TrimSpace(target)
		}
	}
	return out
}

func antigravityBaseModelMetadata(account *Account, modelID string) (UpstreamModelMetadata, bool) {
	if account == nil || !isAntigravityTieredBaseCandidate(modelID) {
		return UpstreamModelMetadata{}, false
	}
	mapping := explicitAntigravityModelMapping(account)
	levelSet := map[string]bool{}
	var metas []UpstreamModelMetadata
	collectVariant := func(variantKey, target string) {
		base, level, ok := splitAntigravityThinkingTierVariant(variantKey)
		if !ok || !strings.EqualFold(base, modelID) {
			return
		}
		if level != "" {
			levelSet[level] = true
		}
		for _, lookup := range []string{target, variantKey} {
			lookup = strings.TrimSpace(lookup)
			if lookup == "" {
				continue
			}
			if meta, found := account.GetUpstreamModelMetadata(lookup); found {
				metas = append(metas, meta)
				break
			}
		}
	}

	var baseTarget string
	if resolved, matched := account.ResolveMappedModel(modelID); matched {
		baseTarget = strings.TrimSpace(resolved)
	}

	if baseTarget != "" && !strings.EqualFold(baseTarget, modelID) {
		collectVariant(baseTarget, baseTarget)
	} else {
		type pair struct{ key, target string }
		var variants []pair
		for key, target := range mapping {
			key = strings.TrimSpace(key)
			target = strings.TrimSpace(target)
			if key == "" || target == "" {
				continue
			}
			if base, _, ok := splitAntigravityThinkingTierVariant(key); ok && strings.EqualFold(base, modelID) {
				variants = append(variants, pair{key: key, target: target})
			}
		}
		sort.Slice(variants, func(i, j int) bool {
			ri, rj := antigravityVariantRank(variants[i].key), antigravityVariantRank(variants[j].key)
			if ri != rj {
				return ri < rj
			}
			return variants[i].key < variants[j].key
		})
		for _, v := range variants {
			collectVariant(v.key, v.target)
		}
	}

	if len(metas) == 0 {
		return UpstreamModelMetadata{}, false
	}
	out := UpstreamModelMetadata{ID: modelID}
	for _, meta := range metas {
		if out.DisplayName == "" && strings.TrimSpace(meta.DisplayName) != "" {
			out.DisplayName = stripAntigravityTierDisplaySuffix(meta.DisplayName)
		}
		if out.Description == "" && strings.TrimSpace(meta.Description) != "" {
			out.Description = strings.TrimSpace(meta.Description)
		}
		for _, input := range meta.InputModalities {
			if !stringSliceContains(out.InputModalities, input) {
				out.InputModalities = append(out.InputModalities, input)
			}
		}
		if meta.ContextWindow > out.ContextWindow {
			out.ContextWindow = meta.ContextWindow
		}
		if meta.MaxOutputTokens > out.MaxOutputTokens {
			out.MaxOutputTokens = meta.MaxOutputTokens
		}
		if len(levelSet) == 0 {
			for _, lvl := range meta.SupportedReasoningLevels {
				lvl = strings.ToLower(strings.TrimSpace(lvl))
				if lvl != "" && lvl != "none" && !stringSliceContains(out.SupportedReasoningLevels, lvl) {
					out.SupportedReasoningLevels = append(out.SupportedReasoningLevels, lvl)
				}
			}
			if out.DefaultReasoningLevel == "" && stringSliceContains(out.SupportedReasoningLevels, meta.DefaultReasoningLevel) {
				out.DefaultReasoningLevel = meta.DefaultReasoningLevel
			}
		}
	}
	for _, lvl := range []string{"low", "medium", "high", "xhigh"} {
		if levelSet[lvl] {
			out.SupportedReasoningLevels = append(out.SupportedReasoningLevels, lvl)
		}
	}
	if len(out.SupportedReasoningLevels) > 0 {
		reasoningTrue := true
		out.Reasoning = &reasoningTrue
		if out.DefaultReasoningLevel == "" {
			for _, pref := range []string{"high", "medium", "low", "xhigh"} {
				if stringSliceContains(out.SupportedReasoningLevels, pref) {
					out.DefaultReasoningLevel = pref
					break
				}
			}
		}
	}
	return out, true
}
