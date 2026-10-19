package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newToolsTestManager(t *testing.T, configs []ProviderConfig, transport toolsRoundTripper) (*Manager, *miniredis.Miniredis) {
	t.Helper()
	s := miniredis.RunT(t)
	r := redis.NewClient(&redis.Options{Addr: s.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = r.Close() })
	m := NewManager(configs, r)
	m.clientCache[""] = &http.Client{Transport: transport}
	return m, s
}

func TestSearchToolsTavilyFallbackClampsCountWithoutExtraCalls(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "tools", true: "legacy unchanged"}[legacy], func(t *testing.T) {
			var hosts []string
			var sentCount int
			m, _ := newToolsTestManager(t, []ProviderConfig{
				{Type: ProviderTypeDeepSeek, APIKey: "key"},
				{Type: ProviderTypeTavily, APIKey: "key", QuotaLimit: 10},
			}, func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				if r.URL.Host == "api.deepseek.com" {
					return toolsHTTPResponse(http.StatusPaymentRequired, `{}`), nil
				}
				var payload struct {
					MaxResults int `json:"max_results"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				sentCount = payload.MaxResults
				return toolsHTTPResponse(200, `{"results":[{"url":"u","title":"t","content":"s"}]}`), nil
			})
			search := m.SearchTools
			if legacy {
				search = m.SearchWithBestProvider
			}
			resp, provider, err := search(context.Background(), SearchRequest{Query: "q", MaxResults: 50})
			require.NoError(t, err)
			require.Equal(t, ProviderTypeTavily, provider)
			require.Len(t, resp.Results, 1)
			if legacy {
				require.Equal(t, 50, sentCount)
				require.Equal(t, []string{"api.tavily.com"}, hosts)
			} else {
				require.Equal(t, 20, sentCount)
				require.Equal(t, []string{"api.deepseek.com", "api.tavily.com"}, hosts)
			}
			used, err := m.GetUsage(context.Background(), ProviderTypeTavily)
			require.NoError(t, err)
			require.EqualValues(t, 1, used)
		})
	}
}

func TestSearchWithBestProviderExcludesDeepSeek(t *testing.T) {
	for _, tc := range []struct {
		name         string
		legacy       []ProviderConfig
		wantProvider string
		wantHost     string
	}{
		{name: "deepseek and brave", legacy: []ProviderConfig{{Type: ProviderTypeBrave, APIKey: "key"}}, wantProvider: ProviderTypeBrave, wantHost: "api.search.brave.com"},
		{name: "brave quota preferred", legacy: []ProviderConfig{{Type: ProviderTypeTavily, APIKey: "key"}, {Type: ProviderTypeBrave, APIKey: "key", QuotaLimit: 20}}, wantProvider: ProviderTypeBrave, wantHost: "api.search.brave.com"},
		{name: "tavily quota preferred", legacy: []ProviderConfig{{Type: ProviderTypeBrave, APIKey: "key"}, {Type: ProviderTypeTavily, APIKey: "key", QuotaLimit: 20}}, wantProvider: ProviderTypeTavily, wantHost: "api.tavily.com"},
		{name: "deepseek only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configs := append([]ProviderConfig{{Type: ProviderTypeDeepSeek, APIKey: "key", QuotaLimit: 1000}}, tc.legacy...)
			var hosts []string
			m, _ := newToolsTestManager(t, configs, func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				return toolsHTTPResponse(200, `{"results":[{"url":"u","content":"s"}],"web":{"results":[{"url":"u","description":"s"}]}}`), nil
			})
			resp, provider, err := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "q"})
			if tc.wantProvider == "" {
				require.ErrorContains(t, err, "no available provider")
				require.Empty(t, hosts)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantProvider, provider)
				require.Len(t, resp.Results, 1)
				require.Equal(t, []string{tc.wantHost}, hosts)
			}
			used, err := m.GetUsage(context.Background(), ProviderTypeDeepSeek)
			require.NoError(t, err)
			require.Zero(t, used)
		})
	}
}

func TestSearchToolsPreferredOrderAndEmptySuccess(t *testing.T) {
	for _, status := range []int{200, 402, 429, 500, 503, 400} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var hosts []string
			m, _ := newToolsTestManager(t, []ProviderConfig{
				{Type: "tavily", APIKey: "secret", QuotaLimit: 20},
				{Type: "brave", APIKey: "secret", QuotaLimit: 10},
				{Type: "deepseek", APIKey: "secret", QuotaLimit: 5},
			}, func(r *http.Request) (*http.Response, error) {
				hosts = append(hosts, r.URL.Host)
				if r.URL.Host == "api.deepseek.com" {
					if status == 200 {
						return toolsHTTPResponse(200, `{"results":[]}`), nil
					}
					return toolsHTTPResponse(status, "private upstream body secret"), nil
				}
				if r.URL.Host == "api.search.brave.com" {
					return toolsHTTPResponse(503, "secret"), nil
				}
				return toolsHTTPResponse(200, `{"results":[{"url":"https://example.com","title":"t","content":"s"}]}`), nil
			})
			resp, provider, err := m.SearchTools(context.Background(), SearchRequest{Query: "test"})
			if status == 400 {
				var upstream *UpstreamError
				require.ErrorAs(t, err, &upstream)
				require.Equal(t, 400, upstream.StatusCode)
				require.NotContains(t, err.Error(), "secret")
				require.Len(t, hosts, 1)
			} else {
				require.NoError(t, err)
				if status == 200 {
					require.Equal(t, "deepseek", provider)
					require.Empty(t, resp.Results)
					require.Len(t, hosts, 1)
				} else {
					require.Equal(t, "tavily", provider)
					require.Len(t, resp.Results, 1)
					require.Equal(t, []string{"api.deepseek.com", "api.search.brave.com", "api.tavily.com"}, hosts)
				}
			}
			for _, name := range []string{"deepseek", "brave"} {
				used, err := m.GetUsage(context.Background(), name)
				require.NoError(t, err)
				require.Zero(t, used)
			}
			used, err := m.GetUsage(context.Background(), "tavily")
			require.NoError(t, err)
			if provider == "tavily" {
				require.EqualValues(t, 1, used)
			} else {
				require.Zero(t, used)
			}
		})
	}
}

func TestSearchToolsFailClosedAndGlobalRateGuard(t *testing.T) {
	calls := 0
	transport := toolsRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return toolsHTTPResponse(200, `{"results":[{"url":"u","title":"t","snippet":"s"}]}`), nil
	})
	m, s := newToolsTestManager(t, []ProviderConfig{{Type: "deepseek", APIKey: "key-a"}}, transport)
	other := NewManager([]ProviderConfig{{Type: "deepseek", APIKey: "key-b"}}, m.redis)
	other.clientCache[""] = &http.Client{Transport: transport}
	_, _, err := m.SearchTools(context.Background(), SearchRequest{Query: "test"})
	require.NoError(t, err)
	_, _, err = other.SearchTools(context.Background(), SearchRequest{Query: "another account"})
	require.ErrorIs(t, err, ErrRateLimited)
	require.Equal(t, 1, calls)
	s.FastForward(200 * time.Millisecond)
	_, _, err = other.SearchTools(context.Background(), SearchRequest{Query: "test"})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	s.SetError("private redis error")
	_, _, err = m.SearchTools(context.Background(), SearchRequest{Query: "test"})
	require.ErrorIs(t, err, ErrStorageUnavailable)
	require.NotContains(t, err.Error(), "private")
	require.Equal(t, 2, calls)
	m.redis = nil
	_, _, err = m.SearchTools(context.Background(), SearchRequest{Query: "test"})
	require.ErrorIs(t, err, ErrStorageUnavailable)
	require.Equal(t, 2, calls)
}

func TestSearchToolsQuotaAndTransportFallback(t *testing.T) {
	calls := 0
	m, _ := newToolsTestManager(t, []ProviderConfig{
		{Type: "deepseek", APIKey: "key", QuotaLimit: 1},
		{Type: "brave", APIKey: "key", QuotaLimit: 1},
	}, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "api.deepseek.com" {
			return nil, errors.New("private key")
		}
		return toolsHTTPResponse(200, `{"web":{"results":[{"url":"u"}]}}`), nil
	})
	_, name, err := m.SearchTools(context.Background(), SearchRequest{Query: "test"})
	require.NoError(t, err)
	require.Equal(t, "brave", name)
	require.Equal(t, 2, calls)
	_, _, err = m.SearchTools(context.Background(), SearchRequest{Query: "test"})
	require.Error(t, err)
	require.Equal(t, 2, calls)
}

func TestSearchToolsConcurrentQuotaAndCancellation(t *testing.T) {
	started := make(chan struct{})
	m, _ := newToolsTestManager(t, []ProviderConfig{{Type: "brave", APIKey: "key", QuotaLimit: 1}}, func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := m.SearchTools(ctx, SearchRequest{Query: "first"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request never started")
	}
	_, _, err := m.SearchTools(context.Background(), SearchRequest{Query: "second"})
	require.ErrorIs(t, err, ErrNoAvailableProvider)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	used, err := m.GetUsage(context.Background(), "brave")
	require.NoError(t, err)
	require.Zero(t, used)
}

func TestSearchToolsRollbackStorageFailureStopsFallback(t *testing.T) {
	for _, status := range []int{200, 503} {
		var server *miniredis.Miniredis
		calls := 0
		m, s := newToolsTestManager(t, []ProviderConfig{{Type: "brave", APIKey: "key"}, {Type: "tavily", APIKey: "key"}}, func(*http.Request) (*http.Response, error) {
			calls++
			server.SetError("private storage failure")
			return toolsHTTPResponse(status, `{"web":{"results":[]}}`), nil
		})
		server = s
		_, _, err := m.SearchTools(context.Background(), SearchRequest{Query: "q"})
		require.ErrorIs(t, err, ErrStorageUnavailable)
		require.Equal(t, 1, calls)
	}
}

func TestSearchToolsSkipUnavailableAndRateFallback(t *testing.T) {
	past := time.Now().Add(-time.Hour).Unix()
	var hosts []string
	m, s := newToolsTestManager(t, []ProviderConfig{
		{Type: "deepseek", APIKey: ""},
		{Type: "deepseek", APIKey: "expired", ExpiresAt: &past},
		{Type: "deepseek", APIKey: "proxied", ProxyID: 1},
		{Type: "deepseek", APIKey: "key"},
		{Type: "brave", APIKey: "key"},
	}, func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		if r.URL.Host == "api.deepseek.com" {
			return toolsHTTPResponse(200, `{"results":[]}`), nil
		}
		return toolsHTTPResponse(200, `{"web":{"results":[]}}`), nil
	})
	require.NoError(t, s.Set("websearch:proxy_unavailable:1", "1"))
	_, name, err := m.SearchTools(context.Background(), SearchRequest{Query: "first"})
	require.NoError(t, err)
	require.Equal(t, "deepseek", name)
	_, name, err = m.SearchTools(context.Background(), SearchRequest{Query: "second"})
	require.NoError(t, err)
	require.Equal(t, "brave", name)
	require.Equal(t, []string{"api.deepseek.com", "api.search.brave.com"}, hosts)
}
