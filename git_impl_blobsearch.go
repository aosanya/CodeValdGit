package codevaldgit

import (
	"context"
	"fmt"
)

// SearchBlobs performs a ranked full-text search over Blob name and content
// fields using the injected [BlobSearcher]. The search is always agency-scoped;
// RepositoryName is accepted but ignored (the ArangoSearch View indexes all
// repos for the agency). Returns an empty slice without error when no
// BlobSearcher is configured (graceful degradation for backends that don't
// support ArangoSearch Views, e.g. the filesystem backend in tests).
func (m *gitManager) SearchBlobs(ctx context.Context, req SearchBlobsRequest) ([]BlobSearchResult, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("SearchBlobs: query must not be empty")
	}
	if m.searcher == nil {
		return []BlobSearchResult{}, nil
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	return m.searcher.Search(ctx, m.agencyID, req.Query, limit)
}
