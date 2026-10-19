package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

const toolsDeepSeekRateKey = "websearch:rate:deepseek:global"

// Reserve before dispatch so concurrent searches cannot overrun the quota.
// Failed/empty searches release the reservation; successful nonempty searches keep it.
var toolsReserveScript = redis.NewScript(`
local used = tonumber(redis.call('GET', KEYS[1]) or '0')
local limit = tonumber(ARGV[1])
if limit > 0 and used >= limit then return 0 end
redis.call('INCR', KEYS[1])
if redis.call('TTL', KEYS[1]) < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
return 1
`)

var toolsRollbackScript = redis.NewScript(`
local used = tonumber(redis.call('GET', KEYS[1]) or '0')
if used > 0 then redis.call('DECR', KEYS[1]) end
return 1
`)

// SearchTools uses a fixed provider preference for the tools API. Unlike the
// legacy emulation entry, Redis is required and empty results do not use quota.
// Results may be fewer than MaxResults: provider limits apply without issuing
// additional searches (Brave and Tavily each allow at most 20 results per call).
func (m *Manager) SearchTools(ctx context.Context, req SearchRequest) (*SearchResponse, string, error) {
	req, err := normalizeToolsRequest(req)
	if err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if m.redis == nil {
		return nil, "", ErrStorageUnavailable
	}
	lastErr := ErrNoAvailableProvider
	for _, kind := range []string{ProviderTypeDeepSeek, ProviderTypeBrave, ProviderTypeTavily} {
		for _, cfg := range m.configs {
			if cfg.Type != kind || !m.isProviderAvailable(cfg) {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, "", err
			}
			if proxyID := resolveProxyID(cfg, req.ProxyURL); proxyID > 0 {
				unavailable, err := m.redis.Exists(ctx, fmt.Sprintf(proxyUnavailableKey, proxyID)).Result()
				if err != nil {
					return nil, "", ErrStorageUnavailable
				}
				if unavailable != 0 {
					continue
				}
			}
			reserved, err := toolsReserveScript.Run(ctx, m.redis, []string{quotaRedisKey(kind)}, cfg.QuotaLimit, int64(quotaTTLFromSubscription(cfg.SubscribedAt).Seconds())).Int()
			if err != nil {
				return nil, "", ErrStorageUnavailable
			}
			if reserved == 0 {
				continue
			}
			if kind == ProviderTypeDeepSeek {
				// All keys/accounts/manager instances share a conservative 200ms
				// spacing guard because account ownership of keys is unknown.
				allowed, err := m.redis.SetNX(ctx, toolsDeepSeekRateKey, "1", 200*time.Millisecond).Result()
				if err != nil || !allowed {
					rollbackErr := m.releaseToolsQuota(ctx, kind)
					if err != nil || rollbackErr != nil {
						return nil, "", ErrStorageUnavailable
					}
					lastErr = ErrRateLimited
					continue
				}
			}
			resp, searchErr := m.executeToolsSearch(ctx, cfg, req)
			if searchErr != nil || len(resp.Results) == 0 {
				if err := m.releaseToolsQuota(ctx, kind); err != nil {
					return nil, "", err
				}
			}
			if searchErr == nil {
				return resp, kind, nil
			}
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			lastErr = searchErr
			var upstream *UpstreamError
			if !errors.As(searchErr, &upstream) || !toolsCanFallback(upstream.StatusCode) {
				return nil, "", searchErr
			}
		}
	}
	return nil, "", lastErr
}

func toolsCanFallback(status int) bool {
	return status == 0 || status == http.StatusPaymentRequired || status == http.StatusTooManyRequests || status >= 500 && status <= 599
}

func (m *Manager) releaseToolsQuota(ctx context.Context, provider string) error {
	// Caller cancellation must not strand a reservation. Bound cleanup independently.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := toolsRollbackScript.Run(cleanupCtx, m.redis, []string{quotaRedisKey(provider)}).Err(); err != nil {
		return ErrStorageUnavailable
	}
	return nil
}

// toolsTransport strips upstream error bodies before legacy providers can read
// them. Only this entry uses it, leaving legacy emulation behavior unchanged.
type toolsTransport struct {
	base     http.RoundTripper
	provider string
}

func (t toolsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, &UpstreamError{Provider: t.provider}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, &UpstreamError{Provider: t.provider, StatusCode: resp.StatusCode}
	}
	return resp, nil
}

func (m *Manager) executeToolsSearch(ctx context.Context, cfg ProviderConfig, req SearchRequest) (*SearchResponse, error) {
	if cfg.Type == ProviderTypeTavily && req.MaxResults > 20 {
		req.MaxResults = 20
	}
	proxyURL := cfg.ProxyURL
	if req.ProxyURL != "" {
		proxyURL = req.ProxyURL
	}
	client, err := m.getOrCreateHTTPClient(proxyURL)
	if err != nil {
		return nil, &UpstreamError{Provider: cfg.Type}
	}
	copy := *client
	base := copy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = toolsTransport{base: base, provider: cfg.Type}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if cfg.Type == ProviderTypeDeepSeek {
		// DeepSeek already handles and sanitizes HTTP status codes itself.
		copy.Transport = base
	}
	provider := m.buildProvider(cfg, &copy)
	resp, err := provider.Search(ctx, req)
	if err == nil {
		return resp, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return nil, upstream
	}
	return nil, &UpstreamError{Provider: cfg.Type}
}
