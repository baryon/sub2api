package websearch

// SearchResult represents a single web search result.
type SearchResult struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Snippet     string   `json:"snippet"`
	PageAge     string   `json:"page_age,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Content     string   `json:"content,omitempty"`
	SiteName    string   `json:"site_name,omitempty"`
	SiteIcon    string   `json:"site_icon,omitempty"`
	Date        string   `json:"date,omitempty"`
	RerankScore *float64 `json:"rerank_score,omitempty"`
	Authority   *int     `json:"authority,omitempty"`
}

// SearchRequest describes a web search to perform.
type SearchRequest struct {
	Query        string
	MaxResults   int    // legacy providers default <=0 to 5; tools/DeepSeek default 0 to 10 and require 1..50
	ProxyURL     string // optional HTTP proxy URL
	OriQuestion  *string
	NeedContent  bool
	Localization string
}

// SearchResponse holds the results of a web search.
type SearchResponse struct {
	Results []SearchResult
	Query   string // the query that was actually executed
}

const defaultMaxResults = 5

// Provider type identifiers.
const (
	ProviderTypeBrave    = "brave"
	ProviderTypeTavily   = "tavily"
	ProviderTypeDeepSeek = "deepseek"
)
