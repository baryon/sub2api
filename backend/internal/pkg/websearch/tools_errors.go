package websearch

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidRequest      = errors.New("websearch: invalid search request")
	ErrNoAvailableProvider = errors.New("websearch: no available provider")
	ErrRateLimited         = errors.New("websearch: provider rate limited")
	ErrStorageUnavailable  = errors.New("websearch: quota storage unavailable")
)

// UpstreamError contains only safe metadata. StatusCode is zero for transport or
// malformed-response failures; it is an upstream status, not a gateway status.
type UpstreamError struct {
	Provider   string
	StatusCode int
}

func (e *UpstreamError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s: status %d", e.Provider, e.StatusCode)
	}
	return fmt.Sprintf("%s: upstream request failed", e.Provider)
}
