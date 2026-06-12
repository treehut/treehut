// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package bleve

import (
	"context"

	indexer_internal "forgejo.org/modules/indexer/internal"
	inner_bleve "forgejo.org/modules/indexer/internal/bleve"
	"forgejo.org/modules/indexer/issues/internal"
	"forgejo.org/modules/optional"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/token/unicodenorm"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/blevesearch/bleve/v2/search/query"
)

const (
	issueIndexerAnalyzer      = "issueIndexer"
	issueIndexerDocType       = "issueIndexerDocType"
	issueIndexerLatestVersion = 7
)

const unicodeNormalizeName = "unicodeNormalize"

func addUnicodeNormalizeTokenFilter(m *mapping.IndexMappingImpl) error {
	return m.AddCustomTokenFilter(unicodeNormalizeName, map[string]any{
		"type": unicodenorm.Name,
		"form": unicodenorm.NFC,
	})
}

const maxBatchSize = 16

// IndexerData an update to the issue indexer
type IndexerData internal.IndexerData

// Type returns the document type, for bleve's mapping.Classifier interface.
func (i *IndexerData) Type() string {
	return issueIndexerDocType
}

// generateIssueIndexMapping generates the bleve index mapping for issues
func generateIssueIndexMapping() (mapping.IndexMapping, error) {
	mapping := bleve.NewIndexMapping()
	docMapping := bleve.NewDocumentMapping()

	numericFieldMapping := bleve.NewNumericFieldMapping()
	numericFieldMapping.Store = false
	numericFieldMapping.IncludeInAll = false
	docMapping.AddFieldMappingsAt("repo_id", numericFieldMapping)

	textFieldMapping := bleve.NewTextFieldMapping()
	textFieldMapping.Store = false
	textFieldMapping.IncludeInAll = false

	boolFieldMapping := bleve.NewBooleanFieldMapping()
	boolFieldMapping.Store = false
	boolFieldMapping.IncludeInAll = false

	numberFieldMapping := bleve.NewNumericFieldMapping()
	numberFieldMapping.Store = false
	numberFieldMapping.IncludeInAll = false

	docMapping.AddFieldMappingsAt("is_public", boolFieldMapping)

	docMapping.AddFieldMappingsAt("index", numberFieldMapping)
	docMapping.AddFieldMappingsAt("title", textFieldMapping)
	docMapping.AddFieldMappingsAt("content", textFieldMapping)
	docMapping.AddFieldMappingsAt("comments", textFieldMapping)

	docMapping.AddFieldMappingsAt("is_pull", boolFieldMapping)
	docMapping.AddFieldMappingsAt("is_closed", boolFieldMapping)
	docMapping.AddFieldMappingsAt("label_ids", numberFieldMapping)
	docMapping.AddFieldMappingsAt("no_label", boolFieldMapping)
	docMapping.AddFieldMappingsAt("milestone_id", numberFieldMapping)
	docMapping.AddFieldMappingsAt("project_id", numberFieldMapping)
	docMapping.AddFieldMappingsAt("project_board_id", numberFieldMapping)
	docMapping.AddFieldMappingsAt("poster_id", numberFieldMapping)
	docMapping.AddFieldMappingsAt("assignee_ids", numberFieldMapping)
	docMapping.AddFieldMappingsAt("mention_ids", numberFieldMapping)
	docMapping.AddFieldMappingsAt("reviewed_ids", numberFieldMapping)
	docMapping.AddFieldMappingsAt("review_requested_ids", numberFieldMapping)
	docMapping.AddFieldMappingsAt("subscriber_ids", numberFieldMapping)
	docMapping.AddFieldMappingsAt("updated_unix", numberFieldMapping)

	docMapping.AddFieldMappingsAt("created_unix", numberFieldMapping)
	docMapping.AddFieldMappingsAt("deadline_unix", numberFieldMapping)
	docMapping.AddFieldMappingsAt("comment_count", numberFieldMapping)

	if err := addUnicodeNormalizeTokenFilter(mapping); err != nil {
		return nil, err
	} else if err = mapping.AddCustomAnalyzer(issueIndexerAnalyzer, map[string]any{
		"type":          custom.Name,
		"char_filters":  []string{},
		"tokenizer":     unicode.Name,
		"token_filters": []string{unicodeNormalizeName, lowercase.Name},
	}); err != nil {
		return nil, err
	}

	mapping.DefaultAnalyzer = issueIndexerAnalyzer
	mapping.AddDocumentMapping(issueIndexerDocType, docMapping)
	mapping.AddDocumentMapping("_all", bleve.NewDocumentDisabledMapping())
	mapping.DefaultMapping = bleve.NewDocumentDisabledMapping() // disable default mapping, avoid indexing unexpected structs

	return mapping, nil
}

var _ internal.Indexer = &Indexer{}

// Indexer implements Indexer interface
type Indexer struct {
	inner                    *inner_bleve.Indexer
	indexer_internal.Indexer // do not composite inner_bleve.Indexer directly to avoid exposing too much
}

// NewIndexer creates a new bleve local indexer
func NewIndexer(indexDir string) *Indexer {
	inner := inner_bleve.NewIndexer(indexDir, issueIndexerLatestVersion, generateIssueIndexMapping)
	return &Indexer{
		Indexer: inner,
		inner:   inner,
	}
}

// Index will save the index data
func (b *Indexer) Index(_ context.Context, issues ...*internal.IndexerData) error {
	batch := inner_bleve.NewFlushingBatch(b.inner.Indexer, maxBatchSize)
	for _, issue := range issues {
		if err := batch.Index(indexer_internal.Base36(issue.ID), (*IndexerData)(issue)); err != nil {
			return err
		}
	}
	return batch.Flush()
}

// Delete deletes indexes by ids
func (b *Indexer) Delete(_ context.Context, ids ...int64) error {
	batch := inner_bleve.NewFlushingBatch(b.inner.Indexer, maxBatchSize)
	for _, id := range ids {
		if err := batch.Delete(indexer_internal.Base36(id)); err != nil {
			return err
		}
	}
	return batch.Flush()
}

func termQuery(token internal.Token) query.Query {
	innerQ := bleve.NewDisjunctionQuery(
		inner_bleve.MatchPhraseQuery(token.Term, "title", issueIndexerAnalyzer, token.Fuzzy, 2.0),
		inner_bleve.MatchPhraseQuery(token.Term, "content", issueIndexerAnalyzer, token.Fuzzy, 1.0),
		inner_bleve.MatchPhraseQuery(token.Term, "comments", issueIndexerAnalyzer, token.Fuzzy, 1.0))

	if issueID, err := token.ParseIssueReference(); err == nil {
		idQuery := inner_bleve.NumericEqualityQuery(issueID, "index")
		idQuery.SetBoost(20.0)
		innerQ.AddQuery(idQuery)
	}
	return innerQ
}

// Create a boolean query with the provided tokens (if any)
func keywordQuery(tokens []internal.Token) *query.BooleanQuery {
	q := bleve.NewBooleanQuery()

	// If there is only a single term the user is likely looking for
	// a MUST rather than a SHOULD
	if len(tokens) == 1 && tokens[0].Kind != internal.BoolOptNot {
		q.AddMust(termQuery(tokens[0]))
		return q
	}

	for _, token := range tokens {
		innerQ := termQuery(token)
		switch token.Kind {
		case internal.BoolOptMust:
			q.AddMust(innerQ)
		case internal.BoolOptShould:
			q.AddShould(innerQ)
		case internal.BoolOptNot:
			q.AddMustNot(innerQ)
		}
	}

	return q
}

// Search searches for issues by given conditions.
// Returns the matching issue IDs
func (b *Indexer) Search(ctx context.Context, options *internal.SearchOptions) (*internal.SearchResult, error) {
	q := keywordQuery(options.Tokens)

	var filters []query.Query
	if len(options.RepoIDs) > 0 || options.AllPublic {
		var repoQueries []query.Query
		for _, repoID := range options.RepoIDs {
			repoQueries = append(repoQueries, inner_bleve.NumericEqualityQuery(repoID, "repo_id"))
		}
		if options.AllPublic {
			repoQueries = append(repoQueries, inner_bleve.BoolFieldQuery(true, "is_public"))
		}
		filters = append(filters, bleve.NewDisjunctionQuery(repoQueries...))
	}

	if has, value := options.PriorityRepoID.Get(); has {
		eq := inner_bleve.NumericEqualityQuery(value, "repo_id")
		eq.SetBoost(10.0)
		meh := bleve.NewMatchAllQuery()
		meh.SetBoost(0)
		q.AddShould(bleve.NewDisjunctionQuery(eq, meh))
	}

	if has, value := options.IsPull.Get(); has {
		filters = append(filters, inner_bleve.BoolFieldQuery(value, "is_pull"))
	}
	if has, value := options.IsClosed.Get(); has {
		filters = append(filters, inner_bleve.BoolFieldQuery(value, "is_closed"))
	}

	if options.NoLabelOnly {
		filters = append(filters, inner_bleve.BoolFieldQuery(true, "no_label"))
	} else {
		if len(options.IncludedLabelIDs) > 0 {
			var includeQueries []query.Query
			for _, labelID := range options.IncludedLabelIDs {
				includeQueries = append(includeQueries, inner_bleve.NumericEqualityQuery(labelID, "label_ids"))
			}
			filters = append(filters, includeQueries...)
		} else if len(options.IncludedAnyLabelIDs) > 0 {
			var includeQueries []query.Query
			for _, labelID := range options.IncludedAnyLabelIDs {
				includeQueries = append(includeQueries, inner_bleve.NumericEqualityQuery(labelID, "label_ids"))
			}
			filters = append(filters, bleve.NewDisjunctionQuery(includeQueries...))
		}
		if len(options.ExcludedLabelIDs) > 0 {
			for _, labelID := range options.ExcludedLabelIDs {
				q.AddMustNot(inner_bleve.NumericEqualityQuery(labelID, "label_ids"))
			}
		}
	}

	if len(options.MilestoneIDs) > 0 {
		var milestoneQueries []query.Query
		for _, milestoneID := range options.MilestoneIDs {
			milestoneQueries = append(milestoneQueries, inner_bleve.NumericEqualityQuery(milestoneID, "milestone_id"))
		}
		filters = append(filters, bleve.NewDisjunctionQuery(milestoneQueries...))
	}

	for key, val := range map[string]optional.Option[int64]{
		"project_id":           options.ProjectID,
		"project_board_id":     options.ProjectColumnID,
		"poster_id":            options.PosterID,
		"assignee_ids":         options.AssigneeID,
		"mention_ids":          options.MentionID,
		"reviewed_ids":         options.ReviewedID,
		"review_requested_ids": options.ReviewRequestedID,
		"subscriber_ids":       options.SubscriberID,
	} {
		if has, value := val.Get(); has {
			filters = append(filters, inner_bleve.NumericEqualityQuery(value, key))
		}
	}

	if options.UpdatedAfterUnix.Has() || options.UpdatedBeforeUnix.Has() {
		filters = append(filters, inner_bleve.NumericRangeInclusiveQuery(
			options.UpdatedAfterUnix,
			options.UpdatedBeforeUnix,
			"updated_unix"))
	}

	switch len(filters) {
	case 0:
		break
	case 1:
		q.Filter = filters[0]
	default:
		q.Filter = bleve.NewConjunctionQuery(filters...)
	}
	var indexerQuery query.Query = q
	if q.Must == nil && q.MustNot == nil && q.Should == nil && len(filters) == 0 {
		indexerQuery = bleve.NewMatchAllQuery()
	}

	skip, limit := indexer_internal.ParsePaginator(options.Paginator)
	search := bleve.NewSearchRequestOptions(indexerQuery, limit, skip, false)

	if options.SortBy == "" {
		options.SortBy = internal.SortByCreatedAsc
	}

	search.SortBy([]string{string(options.SortBy), "-_id"})

	result, err := b.inner.Indexer.SearchInContext(ctx, search)
	if err != nil {
		return nil, err
	}

	ret := &internal.SearchResult{
		Total: int64(result.Total),
		Hits:  make([]internal.Match, 0, len(result.Hits)),
	}
	for _, hit := range result.Hits {
		id, err := indexer_internal.ParseBase36(hit.ID)
		if err != nil {
			return nil, err
		}
		ret.Hits = append(ret.Hits, internal.Match{
			ID: id,
		})
	}
	return ret, nil
}
