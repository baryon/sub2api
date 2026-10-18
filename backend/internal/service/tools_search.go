package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
)

// SearchTools uses the configured operator-owned search providers without a model account.
func SearchTools(ctx context.Context, request websearch.SearchRequest) (*websearch.SearchResponse, string, error) {
	manager := getWebSearchManager()
	if manager == nil {
		return nil, "", websearch.ErrNoAvailableProvider
	}
	return manager.SearchTools(ctx, request)
}
