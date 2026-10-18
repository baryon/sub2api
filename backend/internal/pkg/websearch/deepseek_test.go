package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type toolsRoundTripper func(*http.Request) (*http.Response, error)

func (f toolsRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func toolsHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestDeepSeekRequestAndRichResults(t *testing.T) {
	question := "original question"
	client := &http.Client{Transport: toolsRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https://api.deepseek.com/v1/tools/search", r.URL.String())
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, map[string]any{"query": "query", "count": float64(10), "ori_question": question, "need_content": true, "localization": "192.0.2.1"}, payload)
		return toolsHTTPResponse(200, `{"results":[{"url":"https://example.com","title":"Title","snippet":"short","summary":"long","content":"body","site_name":"Site","site_icon":"icon","date":"2026-10-18T00:00:00Z","rerank_score":0,"authority":0}]}`), nil
	})}
	p := NewDeepSeekProvider("test-key", client)
	require.Equal(t, ProviderTypeDeepSeek, p.Name())
	resp, err := p.Search(context.Background(), SearchRequest{Query: "query", OriQuestion: &question, NeedContent: true, Localization: "192.0.2.1"})
	require.NoError(t, err)
	require.Equal(t, "query", resp.Query)
	require.Len(t, resp.Results, 1)
	r := resp.Results[0]
	require.Equal(t, "long", r.Summary)
	require.Equal(t, "body", r.Content)
	require.Equal(t, "Site", r.SiteName)
	require.Equal(t, "icon", r.SiteIcon)
	require.Equal(t, "2026-10-18T00:00:00Z", r.Date)
	require.NotNil(t, r.RerankScore)
	require.Equal(t, 0.0, *r.RerankScore)
	require.NotNil(t, r.Authority)
	require.Equal(t, 0, *r.Authority)
	old, err := json.Marshal(SearchResult{URL: "u", Title: "t", Snippet: "s"})
	require.NoError(t, err)
	require.JSONEq(t, `{"url":"u","title":"t","snippet":"s"}`, string(old))
}

func TestDeepSeekValidationAndOptionalFields(t *testing.T) {
	for _, count := range []int{1, 50} {
		client := &http.Client{Transport: toolsRoundTripper(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.EqualValues(t, count, payload["count"])
			require.NotContains(t, payload, "ori_question")
			require.Equal(t, false, payload["need_content"])
			return toolsHTTPResponse(200, `{"results":[{"url":"u","site_name":null,"site_icon":null,"date":null,"authority":-1}]}`), nil
		})}
		resp, err := NewDeepSeekProvider("key", client).Search(context.Background(), SearchRequest{Query: strings.Repeat("界", 128), MaxResults: count})
		require.NoError(t, err)
		require.Empty(t, resp.Results[0].Date)
		require.Equal(t, -1, *resp.Results[0].Authority)
	}
	empty := ""
	long := strings.Repeat("a", 129)
	for _, req := range []SearchRequest{
		{Query: " "}, {Query: long}, {Query: "q", MaxResults: -1}, {Query: "q", MaxResults: 51},
		{Query: "q", OriQuestion: &empty}, {Query: "q", OriQuestion: &long},
	} {
		client := &http.Client{Transport: toolsRoundTripper(func(*http.Request) (*http.Response, error) {
			t.Fatal("invalid input must not reach upstream")
			return nil, nil
		})}
		_, err := NewDeepSeekProvider("key", client).Search(context.Background(), req)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestDeepSeekSafeErrors(t *testing.T) {
	for _, status := range []int{302, 400, 401, 402, 429, 500, 503} {
		client := &http.Client{Transport: toolsRoundTripper(func(*http.Request) (*http.Response, error) {
			return toolsHTTPResponse(status, "private key and response body"), nil
		})}
		_, err := NewDeepSeekProvider("private-key", client).Search(context.Background(), SearchRequest{Query: "q"})
		var upstream *UpstreamError
		require.ErrorAs(t, err, &upstream)
		require.Equal(t, status, upstream.StatusCode)
		require.NotContains(t, err.Error(), "private")
	}
	for _, body := range []string{"private-key", `{}`, `{"results":null}`, `{"results":{}}`, strings.Repeat("a", maxResponseSize+1)} {
		client := &http.Client{Transport: toolsRoundTripper(func(*http.Request) (*http.Response, error) {
			return toolsHTTPResponse(200, body), nil
		})}
		_, err := NewDeepSeekProvider("private-key", client).Search(context.Background(), SearchRequest{Query: "q"})
		var upstream *UpstreamError
		require.ErrorAs(t, err, &upstream)
		require.Zero(t, upstream.StatusCode)
		require.NotContains(t, err.Error(), "private")
	}
	client := &http.Client{Transport: toolsRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("private-key") })}
	_, err := NewDeepSeekProvider("private-key", client).Search(context.Background(), SearchRequest{Query: "q"})
	var upstream *UpstreamError
	require.ErrorAs(t, err, &upstream)
	require.NotContains(t, err.Error(), "private")
}

func TestDeepSeekCanceledContextDoesNotDispatch(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: toolsRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return toolsHTTPResponse(200, `{"results":[]}`), nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDeepSeekProvider("key", client).Search(ctx, SearchRequest{Query: "q"})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
}

func TestManagerTestSearchDeepSeekFactory(t *testing.T) {
	m := NewManager([]ProviderConfig{{Type: ProviderTypeDeepSeek, APIKey: "key"}}, nil)
	m.clientCache[""] = &http.Client{Transport: toolsRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "api.deepseek.com", r.URL.Host)
		return toolsHTTPResponse(200, `{"results":[]}`), nil
	})}
	_, provider, err := m.TestSearch(context.Background(), SearchRequest{Query: "q"})
	require.NoError(t, err)
	require.Equal(t, ProviderTypeDeepSeek, provider)
}
