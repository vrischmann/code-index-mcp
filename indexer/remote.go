package indexer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sourcegraph/zoekt"
)

// RemoteSearcher queries an external zoekt webserver via its HTTP JSON API.
type RemoteSearcher struct {
	baseURL string
	client  *http.Client
}

// NewRemoteSearcher creates a searcher backed by an external zoekt HTTP JSON API.
func NewRemoteSearcher(baseURL string) *RemoteSearcher {
	return &RemoteSearcher{
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

type remoteSearchRequest struct {
	Q    string               `json:"Q"`
	Opts *zoekt.SearchOptions `json:"Opts,omitempty"`
}

type remoteSearchResponse struct {
	Result *zoekt.SearchResult `json:"Result"`
}

// Search calls POST {baseURL}/api/search and returns the result.
func (r *RemoteSearcher) Search(ctx context.Context, queryStr string, opts *zoekt.SearchOptions) (*zoekt.SearchResult, error) {
	body, err := json.Marshal(remoteSearchRequest{Q: queryStr, Opts: opts})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/api/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to zoekt failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zoekt returned status %d", resp.StatusCode)
	}

	var reply remoteSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if reply.Result == nil {
		return &zoekt.SearchResult{}, nil
	}
	return reply.Result, nil
}
