package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// A long conversation keeps its upstream account (and the prompt cache that lives
// there). Slow first tokens that are explained by a large input or by thinking are
// not a reason to move; errors, rate limits and quota still are.

func TestStickyEscape_LargeInputExplainsSlowFirstToken(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	scheduler := &defaultOpenAIAccountScheduler{stats: stats}
	cfg := openAIStickyEscapeConfig{enabled: true, ttftMs: 15000, errorRate: 0.5}

	slow := 20000
	for i := 0; i < 6; i++ {
		stats.reportWithLoad(1, true, &slow, 150000, "")
	}
	_, _, _, shouldEscape := scheduler.shouldEscapeStickyAccount(1, cfg)
	require.False(t, shouldEscape, "20s for a 150k-token input is normal and must not move the conversation")

	for i := 0; i < 6; i++ {
		stats.reportWithLoad(2, true, &slow, 1000, "")
	}
	reason, _, _, shouldEscape := scheduler.shouldEscapeStickyAccount(2, cfg)
	require.True(t, shouldEscape, "20s for a tiny input is a slow account")
	require.Equal(t, "ttft", reason)
}

func TestStickyEscape_ThinkingExplainsSlowFirstToken(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	scheduler := &defaultOpenAIAccountScheduler{stats: stats}
	cfg := openAIStickyEscapeConfig{enabled: true, ttftMs: 15000, errorRate: 0.5}

	slow := 40000
	for i := 0; i < 6; i++ {
		stats.reportWithLoad(1, true, &slow, 20000, "max")
	}
	_, _, _, shouldEscape := scheduler.shouldEscapeStickyAccount(1, cfg)
	require.False(t, shouldEscape)

	for i := 0; i < 6; i++ {
		stats.reportWithLoad(2, true, &slow, 20000, "low")
	}
	_, _, _, shouldEscape = scheduler.shouldEscapeStickyAccount(2, cfg)
	require.True(t, shouldEscape, "40s without thinking is a slow account")
}

func TestStickyEscape_OneStalledRequestDoesNotMoveEveryConversation(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	scheduler := &defaultOpenAIAccountScheduler{stats: stats}
	cfg := openAIStickyEscapeConfig{enabled: true, ttftMs: 15000, errorRate: 0.5}

	fast := 2000
	for i := 0; i < 10; i++ {
		stats.reportWithLoad(1, true, &fast, 5000, "")
	}
	stalled := 120000
	stats.reportWithLoad(1, true, &stalled, 5000, "")
	_, _, _, shouldEscape := scheduler.shouldEscapeStickyAccount(1, cfg)
	require.False(t, shouldEscape)

	for i := 0; i < 6; i++ {
		stats.reportWithLoad(1, true, &stalled, 5000, "")
	}
	_, _, _, shouldEscape = scheduler.shouldEscapeStickyAccount(1, cfg)
	require.True(t, shouldEscape, "a run of stalls is a real problem")
}

func TestStickyEscape_ErrorsStillMoveEvenWhenInputIsLarge(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	scheduler := &defaultOpenAIAccountScheduler{stats: stats}
	cfg := openAIStickyEscapeConfig{enabled: true, ttftMs: 15000, errorRate: 0.5}

	for i := 0; i < 8; i++ {
		stats.reportWithLoad(1, false, nil, 150000, "max")
	}
	reason, _, _, shouldEscape := scheduler.shouldEscapeStickyAccount(1, cfg)
	require.True(t, shouldEscape)
	require.Equal(t, "error_rate", reason)
}

func TestStickyEscape_SchedulingScoreKeepsRawFirstToken(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	slow := 20000
	stats.reportWithLoad(1, true, &slow, 150000, "max")
	_, ttft, ok := stats.snapshot(1)
	require.True(t, ok)
	require.InDelta(t, 20000, ttft, 1e-9, "load balancing still compares real first-token times")
}

func TestReportOpenAIAccountScheduleResultForRequest_UsesInputAndThinking(t *testing.T) {
	svc := &OpenAIGatewayService{
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
	}
	effort := "max"
	ttft := 30000
	result := &OpenAIForwardResult{
		Usage:           OpenAIUsage{InputTokens: 10000, CacheReadInputTokens: 90000},
		ReasoningEffort: &effort,
		FirstTokenMs:    &ttft,
	}
	for i := 0; i < 6; i++ {
		svc.ReportOpenAIAccountScheduleResultForRequest(&Account{ID: 7}, "gpt-6-luna", true, result)
	}
	scheduler := &defaultOpenAIAccountScheduler{stats: svc.openaiAccountStats}
	_, _, _, shouldEscape := scheduler.shouldEscapeStickyAccount(7, openAIStickyEscapeConfig{enabled: true, ttftMs: 15000, errorRate: 0.5})
	require.False(t, shouldEscape)
	_, rawTTFT, _ := svc.openaiAccountStats.snapshot(7)
	require.InDelta(t, 30000, rawTTFT, 1e-6)
}

// countingConcurrencyCache refuses the first `busyFor` attempts on every account.
type countingConcurrencyCache struct {
	schedulerTestConcurrencyCache
	attempts atomic.Int64
	busyFor  int64
}

func (c *countingConcurrencyCache) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	n := c.attempts.Add(1)
	if accountID == 31001 && n <= c.busyFor {
		return false, nil
	}
	return true, nil
}

func stickyWaitFixture(t *testing.T, waitMs int, busyFor int64) (*OpenAIGatewayService, *countingConcurrencyCache, *int64) {
	t.Helper()
	groupID := int64(31000)
	accounts := []Account{
		{ID: 31001, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}},
		{ID: 31002, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:long_conversation": 31001}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	cfg.Gateway.OpenAIScheduler.StickyEscapeWaitMs = waitMs
	cc := &countingConcurrencyCache{busyFor: busyFor}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                cfg,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(cc),
	}
	groupPtr := groupID
	return svc, cc, &groupPtr
}

func TestStickyEscape_FullAccountFreesUpWithinWaitKeepsConversation(t *testing.T) {
	svc, _, groupID := stickyWaitFixture(t, 2000, 3)
	start := time.Now()
	selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), groupID, "", "long_conversation", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(31001), selection.Account.ID)
	require.True(t, selection.Acquired)
	require.True(t, decision.StickySessionHit)
	require.Less(t, time.Since(start), 1500*time.Millisecond)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestStickyEscape_FullAccountStaysFullMovesAfterWait(t *testing.T) {
	svc, _, groupID := stickyWaitFixture(t, 150, 1<<30)
	start := time.Now()
	selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), groupID, "", "long_conversation", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(31002), selection.Account.ID)
	require.False(t, decision.StickySessionHit)
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond, "waited before moving")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestStickyEscape_NoWaitConfiguredMovesImmediately(t *testing.T) {
	svc, cc, groupID := stickyWaitFixture(t, 0, 1<<30)
	selection, _, err := svc.SelectAccountWithScheduler(context.Background(), groupID, "", "long_conversation", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(31002), selection.Account.ID)
	require.LessOrEqual(t, cc.attempts.Load(), int64(3))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestStickyEscape_ContextCancelStopsWaiting(t *testing.T) {
	svc, _, groupID := stickyWaitFixture(t, 5000, 1<<30)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _ = svc.SelectAccountWithScheduler(ctx, groupID, "", "long_conversation", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.Less(t, time.Since(start), 2*time.Second)
}
