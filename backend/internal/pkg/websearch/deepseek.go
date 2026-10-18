package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

const deepSeekSearchEndpoint = "https://api.deepseek.com/v1/tools/search"

type DeepSeekProvider struct {
	apiKey     string
	httpClient *http.Client
}

func NewDeepSeekProvider(apiKey string, client *http.Client) *DeepSeekProvider {
	if client == nil {
		client = &http.Client{Timeout: searchRequestTimeout}
	}
	// Search credentials must never be forwarded by a redirect.
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &DeepSeekProvider{apiKey: apiKey, httpClient: &copy}
}

func (p *DeepSeekProvider) Name() string { return ProviderTypeDeepSeek }

func normalizeToolsRequest(req SearchRequest) (SearchRequest, error) {
	if strings.TrimSpace(req.Query) == "" || !utf8.ValidString(req.Query) || utf8.RuneCountInString(req.Query) > 128 {
		return req, ErrInvalidRequest
	}
	if req.OriQuestion != nil && (!utf8.ValidString(*req.OriQuestion) || utf8.RuneCountInString(*req.OriQuestion) < 1 || utf8.RuneCountInString(*req.OriQuestion) > 128) {
		return req, ErrInvalidRequest
	}
	if req.MaxResults == 0 {
		req.MaxResults = 10
	}
	if req.MaxResults < 1 || req.MaxResults > 50 {
		return req, ErrInvalidRequest
	}
	return req, nil
}

func (p *DeepSeekProvider) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, err := normalizeToolsRequest(req)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Query        string  `json:"query"`
		Count        int     `json:"count"`
		OriQuestion  *string `json:"ori_question,omitempty"`
		NeedContent  bool    `json:"need_content"`
		Localization string  `json:"localization,omitempty"`
	}{req.Query, req.MaxResults, req.OriQuestion, req.NeedContent, req.Localization}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekSearchEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalidRequest
	}
	r.Header.Set("Authorization", "Bearer "+p.apiKey)
	r.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &UpstreamError{Provider: p.Name()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, &UpstreamError{Provider: p.Name(), StatusCode: resp.StatusCode}
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return nil, &UpstreamError{Provider: p.Name()}
	}
	var raw struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.Results == nil {
		return nil, &UpstreamError{Provider: p.Name()}
	}
	return &SearchResponse{Results: raw.Results, Query: req.Query}, nil
}
