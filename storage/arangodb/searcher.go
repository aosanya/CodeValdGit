package arangodb

import (
	"context"
	"fmt"

	driver "github.com/arangodb/go-driver"

	codevaldgit "github.com/aosanya/CodeValdGit"
	"github.com/aosanya/CodeValdSharedLib/arangoutil"
)

// ArangoBlobSearcher implements [codevaldgit.BlobSearcher] by querying the
// ArangoSearch View "git_blob_search_view" which indexes the name and content
// fields of the git_blobs collection. Results are ranked by BM25 relevance.
type ArangoBlobSearcher struct {
	db driver.Database
}

// NewArangoBlobSearcher constructs an ArangoBlobSearcher by opening a
// dedicated ArangoDB connection using cfg. Call [EnsureBlobSearchView] first
// to ensure the View and analyzer exist before issuing queries.
func NewArangoBlobSearcher(ctx context.Context, cfg Config) (*ArangoBlobSearcher, error) {
	db, err := arangoutil.Connect(ctx, arangoutil.Config{
		Endpoint: cfg.Endpoint,
		Username: cfg.Username,
		Password: cfg.Password,
		Database: cfg.Database,
	})
	if err != nil {
		return nil, fmt.Errorf("NewArangoBlobSearcher: connect: %w", err)
	}
	return &ArangoBlobSearcher{db: db}, nil
}

const blobSearchViewName = "git_blob_search_view"

// Search executes a BM25-ranked ArangoSearch query against [blobSearchViewName],
// filtering by agency_id and returning at most limit results.
func (s *ArangoBlobSearcher) Search(ctx context.Context, agencyID, query string, limit int) ([]codevaldgit.BlobSearchResult, error) {
	aql := `
FOR doc IN @@view
  SEARCH ANALYZER(
    doc.properties.name IN TOKENS(@query, "text_en") OR
    doc.properties.content IN TOKENS(@query, "text_en"),
    "text_en"
  )
  FILTER doc.agency_id == @agencyID
  SORT BM25(doc) DESC
  LIMIT @limit
  LET raw = doc.properties
  RETURN {
    id:        doc._key,
    path:      raw.path,
    name:      raw.name,
    extension: raw.extension,
    snippet:   SUBSTRING(raw.content, 0, 200),
    score:     BM25(doc)
  }
`
	bindVars := map[string]any{
		"@view":   blobSearchViewName,
		"query":   query,
		"agencyID": agencyID,
		"limit":   limit,
	}
	cursor, err := s.db.Query(ctx, aql, bindVars)
	if err != nil {
		return nil, fmt.Errorf("SearchBlobs: query: %w", err)
	}
	defer cursor.Close()

	var results []codevaldgit.BlobSearchResult
	for cursor.HasMore() {
		var row struct {
			ID        string  `json:"id"`
			Path      string  `json:"path"`
			Name      string  `json:"name"`
			Extension string  `json:"extension"`
			Snippet   string  `json:"snippet"`
			Score     float64 `json:"score"`
		}
		if _, err := cursor.ReadDocument(ctx, &row); err != nil {
			return nil, fmt.Errorf("SearchBlobs: read: %w", err)
		}
		results = append(results, codevaldgit.BlobSearchResult{
			ID:        row.ID,
			Path:      row.Path,
			Name:      row.Name,
			Extension: row.Extension,
			Snippet:   row.Snippet,
			Score:     row.Score,
		})
	}
	return results, nil
}

// EnsureBlobSearchView creates the ArangoSearch View "git_blob_search_view"
// (if it does not yet exist) over the git_blobs collection, indexing the
// properties.name and properties.content fields with the built-in text_en
// analyzer. The call is idempotent — if the view already exists it is left
// unchanged.
//
// Call this once at startup after [NewBackend] succeeds, before any
// SearchBlobs queries are issued.
func EnsureBlobSearchView(ctx context.Context, cfg Config) error {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://localhost:8529"
	}
	if cfg.Username == "" {
		cfg.Username = "root"
	}
	db, err := arangoutil.Connect(ctx, arangoutil.Config{
		Endpoint: cfg.Endpoint,
		Username: cfg.Username,
		Password: cfg.Password,
		Database: cfg.Database,
	})
	if err != nil {
		return fmt.Errorf("EnsureBlobSearchView: connect: %w", err)
	}

	exists, err := db.ViewExists(ctx, blobSearchViewName)
	if err != nil {
		return fmt.Errorf("EnsureBlobSearchView: check view: %w", err)
	}
	if exists {
		return nil
	}

	_, err = db.CreateArangoSearchView(ctx, blobSearchViewName, &driver.ArangoSearchViewProperties{
		Links: driver.ArangoSearchLinks{
			"git_blobs": driver.ArangoSearchElementProperties{
				Fields: map[string]driver.ArangoSearchElementProperties{
					"properties.name": {
						Analyzers: []string{"text_en"},
					},
					"properties.content": {
						Analyzers: []string{"text_en"},
					},
				},
			},
		},
	})
	if err != nil {
		// Concurrent startup — another instance may have created it.
		if driver.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("EnsureBlobSearchView: create view: %w", err)
	}
	return nil
}
