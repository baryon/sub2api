package service

import "github.com/tidwall/gjson"

// compactionIterationUsage is the token use of the compaction sampling that Claude Platform reports under
// usage.iterations (entries of type "compaction"). The top-level usage of the response leaves it out, yet it is
// billed like any other sampling (TASK-71).
type compactionIterationUsage struct {
	input         int
	output        int
	cacheCreation int
	cacheRead     int
	cache5m       int
	cache1h       int
}

func (c compactionIterationUsage) isZero() bool { return c == compactionIterationUsage{} }

// compactionIterationsFromUsageNode sums the compaction entries of usage.iterations. The second result is false
// when the usage carries no iterations array at all (nothing to replace).
func compactionIterationsFromUsageNode(usageNode gjson.Result) (compactionIterationUsage, bool) {
	iterations := usageNode.Get("iterations")
	if !iterations.IsArray() {
		return compactionIterationUsage{}, false
	}
	var sum compactionIterationUsage
	for _, it := range iterations.Array() {
		if it.Get("type").String() != "compaction" {
			continue
		}
		sum.input += int(it.Get("input_tokens").Int())
		sum.output += int(it.Get("output_tokens").Int())
		sum.cacheRead += int(it.Get("cache_read_input_tokens").Int())
		cc5m := int(it.Get("cache_creation.ephemeral_5m_input_tokens").Int())
		cc1h := int(it.Get("cache_creation.ephemeral_1h_input_tokens").Int())
		cc := int(it.Get("cache_creation_input_tokens").Int())
		if cc == 0 {
			cc = cc5m + cc1h
		}
		sum.cacheCreation += cc
		sum.cache5m += cc5m
		sum.cache1h += cc1h
		if cc5m+cc1h == 0 && cc > 0 {
			// No TTL split reported: the write counts as the default 5 minute class.
			sum.cache5m += cc
		}
	}
	return sum, true
}

// setCompactionIterations records the compaction sampling of one usage object. Usage objects repeat the whole
// table, so a later object replaces the earlier one instead of adding to it.
func (u *ClaudeUsage) setCompactionIterations(usageNode gjson.Result) {
	if u == nil {
		return
	}
	if sum, ok := compactionIterationsFromUsageNode(usageNode); ok {
		u.compaction = sum
	}
}

// foldCompactionIterations adds the recorded compaction sampling to the billed token counts, once.
func (u *ClaudeUsage) foldCompactionIterations() {
	if u == nil || u.compaction.isZero() {
		return
	}
	c := u.compaction
	u.compaction = compactionIterationUsage{}
	// A write reported without a TTL split stays without one, so the sum keeps meaning "all default class".
	keepUnsplit := u.CacheCreationInputTokens > 0 && u.CacheCreation5mTokens+u.CacheCreation1hTokens == 0
	u.InputTokens += c.input
	u.OutputTokens += c.output
	u.CacheReadInputTokens += c.cacheRead
	u.CacheCreationInputTokens += c.cacheCreation
	if !keepUnsplit {
		u.CacheCreation5mTokens += c.cache5m
		u.CacheCreation1hTokens += c.cache1h
	}
}

// hasCompactionIterations reports whether compaction sampling is waiting to be folded in.
func (u *ClaudeUsage) hasCompactionIterations() bool {
	return u != nil && !u.compaction.isZero()
}

// billsCompactionSampling reports whether the compaction sampling under usage.iterations is billed for this
// account. Only API-key accounts (the Otoha group's Claude Platform key) do; OAuth and setup-token accounts keep
// the usage they always had.
func billsCompactionSampling(account *Account) bool {
	return account != nil && account.Type == AccountTypeAPIKey
}
