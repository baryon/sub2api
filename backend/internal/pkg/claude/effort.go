package claude

import (
	"strings"
	"unicode"
)

// Effort levels advertised by Anthropic's output_config.effort parameter.
// Source: https://platform.claude.com/docs/en/build-with-claude/effort
const (
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
	EffortXHigh  = "xhigh"
	EffortMax    = "max"
)

var (
	effortLowMediumHigh         = []string{EffortLow, EffortMedium, EffortHigh}
	effortLowMediumHighMax      = []string{EffortLow, EffortMedium, EffortHigh, EffortMax}
	effortLowMediumHighXHighMax = []string{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
)

// claudeEffortFamilies lists Anthropic models that accept output_config.effort.
// A model matches when its normalized ID equals the family or is the family
// plus a hyphenated suffix (dated IDs, thinking variants).
//
// xhigh is newer than max: Opus 4.6 / Sonnet 4.6 support max but not xhigh.
var claudeEffortFamilies = []struct {
	family string
	levels []string
}{
	{family: "claude-mythos-preview", levels: effortLowMediumHighMax},
	{family: "claude-mythos-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-fable-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-sonnet-4-6", levels: effortLowMediumHighMax},
	{family: "claude-sonnet-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-8", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-7", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-6", levels: effortLowMediumHighMax},
	{family: "claude-opus-4-5", levels: effortLowMediumHigh},
	{family: "claude-opus-5", levels: effortLowMediumHighXHighMax},
}

var effortRank = map[string]int{
	EffortLow:    0,
	EffortMedium: 1,
	EffortHigh:   2,
	EffortXHigh:  3,
	EffortMax:    4,
}

// IsClaudeModelID reports whether model looks like a Claude / Anthropic ID.
func IsClaudeModelID(model string) bool {
	return strings.HasPrefix(normalizeClaudeModelID(model), "claude")
}

// EffortLevelsForModel returns the Anthropic effort values this Claude model
// accepts, in ascending order. Unknown or non-Claude models return nil.
func EffortLevelsForModel(model string) []string {
	family, levels := lookupClaudeEffortFamily(model)
	if family == "" {
		return nil
	}
	out := make([]string, len(levels))
	copy(out, levels)
	return out
}

// ClampEffortForModel maps an already-normalized effort onto a value the
// model accepts. Non-Claude models keep the input. Claude models that do not
// support effort at all (Haiku, Sonnet 4.5, …) return empty.
//
// Unsupported values clamp to the next-higher supported level when one exists
// (xhigh → max on Opus 4.6), otherwise the next-lower (max → high on Opus 4.5).
func ClampEffortForModel(model, effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return ""
	}
	if !IsClaudeModelID(model) {
		return effort
	}
	levels := EffortLevelsForModel(model)
	if len(levels) == 0 {
		return ""
	}
	supported := make(map[string]struct{}, len(levels))
	for _, level := range levels {
		supported[level] = struct{}{}
	}
	if _, ok := supported[effort]; ok {
		return effort
	}
	want, ok := effortRank[effort]
	if !ok {
		return ""
	}
	bestHigher := ""
	bestHigherRank := int(^uint(0) >> 1)
	bestLower := ""
	bestLowerRank := -1
	for _, level := range levels {
		rank, known := effortRank[level]
		if !known {
			continue
		}
		if rank > want && rank < bestHigherRank {
			bestHigher = level
			bestHigherRank = rank
		}
		if rank < want && rank > bestLowerRank {
			bestLower = level
			bestLowerRank = rank
		}
	}
	if bestHigher != "" {
		return bestHigher
	}
	return bestLower
}

func lookupClaudeEffortFamily(model string) (string, []string) {
	id := normalizeClaudeModelID(model)
	if id == "" {
		return "", nil
	}
	for _, entry := range claudeEffortFamilies {
		if id == entry.family || strings.HasPrefix(id, entry.family+"-") {
			return entry.family, entry.levels
		}
	}
	return "", nil
}

func normalizeClaudeModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	id = strings.TrimPrefix(id, "models/")
	if slash := strings.IndexByte(id, '/'); slash >= 0 {
		id = strings.TrimSpace(id[slash+1:])
		id = strings.TrimPrefix(id, "models/")
	}
	id = strings.TrimPrefix(id, "anthropic.")
	id = strings.TrimSuffix(id, "-thinking")
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		id = mapped
	}
	id = trimClaudeDateSuffix(id)
	return id
}

func trimClaudeDateSuffix(id string) string {
	if len(id) < 9 {
		return id
	}
	suffix := id[len(id)-9:]
	if suffix[0] != '-' {
		return id
	}
	for _, r := range suffix[1:] {
		if !unicode.IsDigit(r) {
			return id
		}
	}
	return id[:len(id)-9]
}
