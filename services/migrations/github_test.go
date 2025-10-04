// Copyright 2019 The Gitea Authors. All rights reserved.
// Copyright 2018 Jonas Franz. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"os"
	"testing"
	"time"

	"forgejo.org/models/unittest"
	base "forgejo.org/modules/migration"

	"github.com/google/go-github/v64/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGithubDownloaderFilterComments(t *testing.T) {
	GithubLimitRateRemaining = 3 // Wait at 3 remaining since we could have 3 CI in //

	token := os.Getenv("GITHUB_READ_TOKEN")
	fixturePath := "./testdata/github/full_download"
	server := unittest.NewMockWebServer(t, "https://api.github.com", fixturePath, false)
	defer server.Close()

	downloader := NewGithubDownloaderV3(t.Context(), server.URL, true, true, "", "", token, "forgejo", "test_repo")
	err := downloader.RefreshRate()
	require.NoError(t, err)

	var githubComments []*github.IssueComment
	issueID := int64(7)
	iNodeID := "MDEyOklzc3VlQ29tbWVudDE=" // "IssueComment1"
	iBody := "Hello"
	iCreated := new(github.Timestamp)
	iUpdated := new(github.Timestamp)
	iCreated.Time = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	iUpdated.Time = time.Date(2025, 1, 1, 12, 1, 0, 0, time.UTC)
	iAssociation := "COLLABORATOR"
	iURL := "https://api.github.com/repos/forgejo/test_repo/issues/comments/3164032267"
	iHTMLURL := "https://github.com/forgejo/test_repo/issues/1#issuecomment-3164032267"
	iIssueURL := "https://api.github.com/repos/forgejo/test_repo/issues/1"

	githubComments = append(githubComments,
		&github.IssueComment{
			ID:                &issueID,
			NodeID:            &iNodeID,
			Body:              &iBody,
			Reactions:         nil,
			CreatedAt:         iCreated,
			UpdatedAt:         iUpdated,
			AuthorAssociation: &iAssociation,
			URL:               &iURL,
			HTMLURL:           &iHTMLURL,
			IssueURL:          &iIssueURL,
		},
	)

	prID := int64(4)
	pNodeID := "IC_kwDOPQx9Mc65LHhx"
	pBody := "Hello"
	pCreated := new(github.Timestamp)
	pUpdated := new(github.Timestamp)
	pCreated.Time = time.Date(2025, 1, 1, 11, 0, 0, 0, time.UTC)
	pUpdated.Time = time.Date(2025, 1, 1, 11, 1, 0, 0, time.UTC)
	pAssociation := "COLLABORATOR"
	pURL := "https://api.github.com/repos/forgejo/test_repo/issues/comments/3164118916"
	pHTMLURL := "https://github.com/forgejo/test_repo/pull/3#issuecomment-3164118916"
	pIssueURL := "https://api.github.com/repos/forgejo/test_repo/issues/3"

	githubComments = append(githubComments, &github.IssueComment{
		ID:                &prID,
		NodeID:            &pNodeID,
		Body:              &pBody,
		Reactions:         nil,
		CreatedAt:         pCreated,
		UpdatedAt:         pUpdated,
		AuthorAssociation: &pAssociation,
		URL:               &pURL,
		HTMLURL:           &pHTMLURL,
		IssueURL:          &pIssueURL,
	})

	filteredComments := downloader.filterPRComments(githubComments)

	// Check each issue index not being from the PR
	for _, comment := range filteredComments {
		assert.NotEqual(t, *comment.ID, prID)
	}

	filteredComments = downloader.filterIssueComments(githubComments)

	// Check each issue index not being from the issue
	for _, comment := range filteredComments {
		assert.NotEqual(t, *comment.ID, issueID)
	}
}

func TestGitHubDownloadRepo(t *testing.T) {
	GithubLimitRateRemaining = 3 // Wait at 3 remaining since we could have 3 CI in //

	token := os.Getenv("GITHUB_READ_TOKEN")
	fixturePath := "./testdata/github/full_download"
	server := unittest.NewMockWebServer(t, "https://api.github.com", fixturePath, false)
	defer server.Close()

	downloader := NewGithubDownloaderV3(t.Context(), server.URL, true, true, "", "", token, "forgejo", "test_repo")
	err := downloader.RefreshRate()
	require.NoError(t, err)

	repo, err := downloader.GetRepoInfo()
	require.NoError(t, err)
	assertRepositoryEqual(t, &base.Repository{
		Name:          "test_repo",
		Owner:         "forgejo",
		Description:   "Exclusively used for testing Github->Forgejo migration",
		CloneURL:      server.URL + "/forgejo/test_repo.git",
		OriginalURL:   server.URL + "/forgejo/test_repo",
		DefaultBranch: "main",
		Website:       "https://codeberg.org/forgejo/forgejo/",
	}, repo)

	topics, err := downloader.GetTopics()
	require.NoError(t, err)
	assert.Contains(t, topics, "forgejo")

	milestones, err := downloader.GetMilestones()
	require.NoError(t, err)
	assertMilestonesEqual(t, []*base.Milestone{
		{
			Title:       "1.0.0",
			Description: "Version 1",
			Created:     time.Date(2025, 8, 7, 12, 48, 56, 0, time.UTC),
			Updated:     timePtr(time.Date(2025, time.August, 12, 12, 34, 20, 0, time.UTC)),
			State:       "open",
		},
		{
			Title:       "0.9.0",
			Description: "A milestone",
			Deadline:    timePtr(time.Date(2025, 8, 1, 7, 0, 0, 0, time.UTC)),
			Created:     time.Date(2025, 8, 7, 12, 54, 20, 0, time.UTC),
			Updated:     timePtr(time.Date(2025, 8, 12, 11, 29, 52, 0, time.UTC)),
			Closed:      timePtr(time.Date(2025, 8, 7, 12, 54, 38, 0, time.UTC)),
			State:       "closed",
		},
		{
			Title:       "1.1.0",
			Description: "We can do that",
			Deadline:    timePtr(time.Date(2025, 8, 31, 7, 0, 0, 0, time.UTC)),
			Created:     time.Date(2025, 8, 7, 12, 50, 58, 0, time.UTC),
			Updated:     timePtr(time.Date(2025, 8, 7, 12, 53, 15, 0, time.UTC)),
			State:       "open",
		},
	}, milestones)

	labels, err := downloader.GetLabels()
	require.NoError(t, err)
	assertLabelsEqual(t, []*base.Label{
		{
			Name:        "bug",
			Color:       "d73a4a",
			Description: "Something isn't working",
		},
		{
			Name:        "documentation",
			Color:       "0075ca",
			Description: "Improvements or additions to documentation",
		},
		{
			Name:        "duplicate",
			Color:       "cfd3d7",
			Description: "This issue or pull request already exists",
		},
		{
			Name:        "enhancement",
			Color:       "a2eeef",
			Description: "New feature or request",
		},
		{
			Name:        "good first issue",
			Color:       "7057ff",
			Description: "Good for newcomers",
		},
		{
			Name:        "help wanted",
			Color:       "008672",
			Description: "Extra attention is needed",
		},
		{
			Name:        "invalid",
			Color:       "e4e669",
			Description: "This doesn't seem right",
		},
		{
			Name:        "question",
			Color:       "d876e3",
			Description: "Further information is requested",
		},
		{
			Name:        "wontfix",
			Color:       "ffffff",
			Description: "This will not be worked on",
		},
	}, labels)

	id := int64(280443629)
	ct := "application/pdf"
	size := 550175
	dc := 0

	releases, err := downloader.GetReleases()
	require.NoError(t, err)
	assertReleasesEqual(t, []*base.Release{
		{
			TagName:         "v1.0",
			TargetCommitish: "main",
			Name:            "First Release",
			Body:            "Hi, this is the first release! The asset contains the wireguard whitepaper, amazing read for such a simple protocol.",
			Created:         time.Date(2025, time.August, 7, 13, 2, 19, 0, time.UTC),
			Published:       time.Date(2025, time.August, 7, 13, 7, 49, 0, time.UTC),
			PublisherID:     25481501,
			PublisherName:   "Gusted",
			Assets: []*base.ReleaseAsset{
				{
					ID:            id,
					Name:          "wireguard.pdf",
					ContentType:   &ct,
					Size:          &size,
					DownloadCount: &dc,
					Created:       time.Date(2025, time.August, 7, 23, 39, 27, 0, time.UTC),
					Updated:       time.Date(2025, time.August, 7, 23, 39, 29, 0, time.UTC),
				},
			},
		},
	}, releases)

	// downloader.GetIssues()
	issues, isEnd, err := downloader.GetIssues(1, 2)
	require.NoError(t, err)
	assert.False(t, isEnd)
	assertIssuesEqual(t, []*base.Issue{
		{
			Number:     1,
			Title:      "First issue",
			Content:    "This is an issue.",
			PosterID:   37243484,
			PosterName: "PatDyn",
			State:      "open",
			Created:    time.Date(2025, time.August, 7, 12, 44, 7, 0, time.UTC),
			Updated:    time.Date(2025, time.August, 7, 12, 44, 47, 0, time.UTC),
		},
		{
			Number:     2,
			Title:      "Second Issue",
			Content:    "Mentioning #1 ",
			Milestone:  "1.1.0",
			PosterID:   37243484,
			PosterName: "PatDyn",
			State:      "open",
			Created:    time.Date(2025, 8, 7, 12, 45, 44, 0, time.UTC),
			Updated:    time.Date(2025, 8, 7, 13, 7, 25, 0, time.UTC),
			Labels: []*base.Label{
				{
					Name:        "duplicate",
					Color:       "cfd3d7",
					Description: "This issue or pull request already exists",
				},
				{
					Name:        "good first issue",
					Color:       "7057ff",
					Description: "Good for newcomers",
				},
				{
					Name:        "help wanted",
					Color:       "008672",
					Description: "Extra attention is needed",
				},
			},
		},
	}, issues)

	// downloader.GetComments()
	comments, _, err := downloader.GetComments(&base.Issue{Number: 2, ForeignIndex: 2})
	require.NoError(t, err)
	assertCommentsEqual(t, []*base.Comment{
		{
			IssueIndex: 2,
			PosterID:   37243484,
			PosterName: "PatDyn",
			Created:    time.Date(2025, time.August, 7, 13, 7, 25, 0, time.UTC),
			Updated:    time.Date(2025, time.August, 7, 13, 7, 25, 0, time.UTC),
			Content:    "Mentioning #3 \nWith some **bold** *statement*",
			Reactions:  nil,
		},
	}, comments)

	// downloader.GetPullRequests()
	prs, _, err := downloader.GetPullRequests(1, 2)
	require.NoError(t, err)
	assertPullRequestsEqual(t, []*base.PullRequest{
		{
			Number:     3,
			Title:      "Update readme.md",
			Content:    "Added a feature description",
			Milestone:  "1.0.0",
			PosterID:   37243484,
			PosterName: "PatDyn",
			State:      "open",
			Created:    time.Date(2025, time.August, 7, 12, 47, 6, 0, time.UTC),
			Updated:    time.Date(2025, time.August, 12, 13, 16, 49, 0, time.UTC),
			Labels: []*base.Label{
				{
					Name:        "enhancement",
					Color:       "a2eeef",
					Description: "New feature or request",
				},
			},
			PatchURL: server.URL + "/forgejo/test_repo/pull/3.patch",
			Head: base.PullRequestBranch{
				Ref:      "some-feature",
				CloneURL: server.URL + "/forgejo/test_repo.git",
				SHA:      "c608ab3997349219e1510cdb5ddd1e5e82897dfa",
				RepoName: "test_repo",

				OwnerName: "forgejo",
			},
			Base: base.PullRequestBranch{
				Ref:       "main",
				SHA:       "442d28a55b842472c95bead51a4c61f209ac1636",
				OwnerName: "forgejo",
				RepoName:  "test_repo",
			},
			ForeignIndex: 3,
		},
		{
			Number:     7,
			Title:      "Update readme.md",
			Content:    "Adding some text to the readme",
			Milestone:  "1.0.0",
			PosterID:   37243484,
			PosterName: "PatDyn",
			State:      "closed",
			Created:    time.Date(2025, time.August, 7, 13, 1, 36, 0, time.UTC),
			Updated:    time.Date(2025, time.August, 12, 12, 47, 35, 0, time.UTC),
			Closed:     timePtr(time.Date(2025, time.August, 7, 13, 2, 19, 0, time.UTC)),
			MergedTime: timePtr(time.Date(2025, time.August, 7, 13, 2, 19, 0, time.UTC)),
			Labels: []*base.Label{
				{
					Name:        "bug",
					Color:       "d73a4a",
					Description: "Something isn't working",
				},
			},
			PatchURL: server.URL + "/forgejo/test_repo/pull/7.patch",
			Head: base.PullRequestBranch{
				Ref:       "another-feature",
				SHA:       "5638cb8f3278e467fc1eefcac14d3c0d5d91601f",
				RepoName:  "test_repo",
				OwnerName: "forgejo",
				CloneURL:  server.URL + "/forgejo/test_repo.git",
			},
			Base: base.PullRequestBranch{
				Ref:       "main",
				SHA:       "6dd0c6801ddbb7333787e73e99581279492ff449",
				OwnerName: "forgejo",
				RepoName:  "test_repo",
			},
			Merged:         true,
			MergeCommitSHA: "ca43b48ca2c461f9a5cb66500a154b23d07c9f90",
			ForeignIndex:   7,
		},
	}, prs)

	reviews, err := downloader.GetReviews(&base.PullRequest{Number: 3, ForeignIndex: 3})
	require.NoError(t, err)
	assertReviewsEqual(t, []*base.Review{
		{
			ID:           3096999684,
			IssueIndex:   3,
			ReviewerID:   37243484,
			ReviewerName: "PatDyn",
			CommitID:     "c608ab3997349219e1510cdb5ddd1e5e82897dfa",
			CreatedAt:    time.Date(2025, 8, 7, 12, 47, 55, 0, time.UTC),
			State:        base.ReviewStateCommented,
			Comments: []*base.ReviewComment{
				{
					ID:        2260216729,
					InReplyTo: 0,
					Content:   "May want to write more",
					TreePath:  "readme.md",
					DiffHunk:  "@@ -1,3 +1,5 @@\n # Forgejo Test Repo\n \n This repo is used to test migrations\n+\n+Add some feature description.",
					Position:  5,
					Line:      0,
					CommitID:  "c608ab3997349219e1510cdb5ddd1e5e82897dfa",
					PosterID:  37243484,
					CreatedAt: time.Date(2025, 8, 7, 12, 47, 50, 0, time.UTC),
					UpdatedAt: time.Date(2025, 8, 7, 12, 47, 55, 0, time.UTC),
				},
			},
		},
		{
			ID:           3097007243,
			IssueIndex:   3,
			ReviewerID:   37243484,
			ReviewerName: "PatDyn",
			CommitID:     "c608ab3997349219e1510cdb5ddd1e5e82897dfa",
			CreatedAt:    time.Date(2025, 8, 7, 12, 49, 36, 0, time.UTC),
			State:        base.ReviewStateCommented,
			Comments: []*base.ReviewComment{
				{
					ID:        2260221159,
					InReplyTo: 0,
					Content:   "Comment",
					TreePath:  "readme.md",
					DiffHunk:  "@@ -1,3 +1,5 @@\n # Forgejo Test Repo\n \n This repo is used to test migrations",
					Position:  3,
					Line:      0,
					CommitID:  "c608ab3997349219e1510cdb5ddd1e5e82897dfa",
					PosterID:  37243484,
					CreatedAt: time.Date(2025, 8, 7, 12, 49, 36, 0, time.UTC),
					UpdatedAt: time.Date(2025, 8, 7, 12, 49, 36, 0, time.UTC),
				},
			},
		},
	}, reviews)
}

func TestGithubMultiToken(t *testing.T) {
	testCases := []struct {
		desc             string
		token            string
		expectedCloneURL string
	}{
		{
			desc:             "Single Token",
			token:            "single_token",
			expectedCloneURL: "https://oauth2:single_token@github.com",
		},
		{
			desc:             "Multi Token",
			token:            "token1,token2",
			expectedCloneURL: "https://oauth2:token1@github.com",
		},
	}
	factory := GithubDownloaderV3Factory{}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			opts := base.MigrateOptions{CloneAddr: "https://github.com/go-gitea/gitea", AuthToken: tC.token}
			client, err := factory.New(t.Context(), opts)
			require.NoError(t, err)

			cloneURL, err := client.FormatCloneURL(opts, "https://github.com")
			require.NoError(t, err)

			assert.Equal(t, tC.expectedCloneURL, cloneURL)
		})
	}
}
