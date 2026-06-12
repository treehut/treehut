// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues_test

import (
	"testing"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	org_model "forgejo.org/models/organization"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetReviewByID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	review, err := issues_model.GetReviewByID(db.DefaultContext, 1)
	require.NoError(t, err)
	assert.Equal(t, "Demo Review", review.Content)
	assert.Equal(t, issues_model.ReviewTypeApprove, review.Type)

	_, err = issues_model.GetReviewByID(db.DefaultContext, 23892)
	require.Error(t, err)
	assert.True(t, issues_model.IsErrReviewNotExist(err), "IsErrReviewNotExist")
}

func TestReview_LoadAttributes(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 1})
	require.NoError(t, review.LoadAttributes(db.DefaultContext))
	assert.NotNil(t, review.Issue)
	assert.NotNil(t, review.Reviewer)

	invalidReview1 := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 2})
	require.Error(t, invalidReview1.LoadAttributes(db.DefaultContext))

	invalidReview2 := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 3})
	require.Error(t, invalidReview2.LoadAttributes(db.DefaultContext))
}

func TestReview_LoadCodeComments(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	review := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 4})
	require.NoError(t, review.LoadAttributes(db.DefaultContext))
	require.NoError(t, review.LoadCodeComments(db.DefaultContext))
	assert.Len(t, review.CodeComments, 1)
	assert.Equal(t, int64(4), review.CodeComments["README.md"][int64(4)][0].Line)
}

func TestReviewType_Icon(t *testing.T) {
	assert.Equal(t, "check", issues_model.ReviewTypeApprove.Icon())
	assert.Equal(t, "diff", issues_model.ReviewTypeReject.Icon())
	assert.Equal(t, "comment", issues_model.ReviewTypeComment.Icon())
	assert.Equal(t, "comment", issues_model.ReviewTypeUnknown.Icon())
	assert.Equal(t, "dot-fill", issues_model.ReviewTypeRequest.Icon())
	assert.Equal(t, "comment", issues_model.ReviewType(6).Icon())
}

func TestFindReviews(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	reviews, err := issues_model.FindReviews(db.DefaultContext, issues_model.FindReviewOptions{
		Types:      []issues_model.ReviewType{issues_model.ReviewTypeApprove},
		IssueID:    2,
		ReviewerID: 1,
	})
	require.NoError(t, err)
	assert.Len(t, reviews, 1)
	assert.Equal(t, "Demo Review", reviews[0].Content)
}

func TestFindLatestReviews(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	reviews, err := issues_model.FindLatestReviews(db.DefaultContext, issues_model.FindReviewOptions{
		Types:   []issues_model.ReviewType{issues_model.ReviewTypeApprove},
		IssueID: 11,
	})
	require.NoError(t, err)
	assert.Len(t, reviews, 2)
	assert.Equal(t, "duplicate review from user5 (latest)", reviews[0].Content)
	assert.Equal(t, "singular review from org6 and final review for this pr", reviews[1].Content)
}

func TestGetCurrentReview(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	review, err := issues_model.GetCurrentReview(db.DefaultContext, user, issue)
	require.NoError(t, err)
	assert.NotNil(t, review)
	assert.Equal(t, issues_model.ReviewTypePending, review.Type)
	assert.Equal(t, "Pending Review", review.Content)

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 7})
	review2, err := issues_model.GetCurrentReview(db.DefaultContext, user2, issue)
	require.Error(t, err)
	assert.True(t, issues_model.IsErrReviewNotExist(err))
	assert.Nil(t, review2)
}

func TestCreateReview(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	review, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Content:  "New Review",
		Type:     issues_model.ReviewTypePending,
		Issue:    issue,
		Reviewer: user,
	})
	require.NoError(t, err)
	assert.Equal(t, "New Review", review.Content)
	unittest.AssertExistsAndLoadBean(t, &issues_model.Review{Content: "New Review"})
}

func TestGetReviewersByIssueID(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 3})
	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	org3 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 3})
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

	expectedReviews := []*issues_model.Review{}
	expectedReviews = append(expectedReviews,
		&issues_model.Review{
			Reviewer:    org3,
			Type:        issues_model.ReviewTypeReject,
			UpdatedUnix: 946684812,
		},
		&issues_model.Review{
			Reviewer:    user4,
			Type:        issues_model.ReviewTypeApprove,
			UpdatedUnix: 946684813,
		},
		&issues_model.Review{
			Reviewer:    user2,
			Type:        issues_model.ReviewTypeReject,
			UpdatedUnix: 946684814,
		})

	allReviews, err := issues_model.GetReviewsByIssueID(db.DefaultContext, issue.ID)
	require.NoError(t, err)
	for _, review := range allReviews {
		require.NoError(t, review.LoadReviewer(db.DefaultContext))
	}
	if assert.Len(t, allReviews, 3) {
		for i, review := range allReviews {
			assert.Equal(t, expectedReviews[i].Reviewer, review.Reviewer)
			assert.Equal(t, expectedReviews[i].Type, review.Type)
			assert.Equal(t, expectedReviews[i].UpdatedUnix, review.UpdatedUnix)
		}
	}

	allReviews, err = issues_model.GetReviewsByIssueID(db.DefaultContext, issue.ID)
	require.NoError(t, err)
	require.NoError(t, allReviews.LoadReviewers(db.DefaultContext))
	if assert.Len(t, allReviews, 3) {
		for i, review := range allReviews {
			assert.Equal(t, expectedReviews[i].Reviewer, review.Reviewer)
			assert.Equal(t, expectedReviews[i].Type, review.Type)
			assert.Equal(t, expectedReviews[i].UpdatedUnix, review.UpdatedUnix)
		}
	}
}

func TestDismissReview(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	rejectReviewExample := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 9})
	requestReviewExample := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 11})
	approveReviewExample := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 8})
	assert.False(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.False(t, approveReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, rejectReviewExample, true))
	rejectReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 9})
	requestReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 11})
	assert.True(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, requestReviewExample, true))
	rejectReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 9})
	requestReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 11})
	assert.True(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.False(t, approveReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, requestReviewExample, true))
	rejectReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 9})
	requestReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 11})
	assert.True(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.False(t, approveReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, requestReviewExample, false))
	rejectReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 9})
	requestReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 11})
	assert.True(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.False(t, approveReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, requestReviewExample, false))
	rejectReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 9})
	requestReviewExample = unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: 11})
	assert.True(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.False(t, approveReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, rejectReviewExample, false))
	assert.False(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.False(t, approveReviewExample.Dismissed)

	require.NoError(t, issues_model.DismissReview(db.DefaultContext, approveReviewExample, true))
	assert.False(t, rejectReviewExample.Dismissed)
	assert.False(t, requestReviewExample.Dismissed)
	assert.True(t, approveReviewExample.Dismissed)
}

func TestDeleteReview(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})

	review1, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Content:  "Official rejection",
		Type:     issues_model.ReviewTypeReject,
		Official: false,
		Issue:    issue,
		Reviewer: user,
	})
	require.NoError(t, err)

	review2, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Content:  "Official approval",
		Type:     issues_model.ReviewTypeApprove,
		Official: true,
		Issue:    issue,
		Reviewer: user,
	})
	require.NoError(t, err)

	require.NoError(t, issues_model.DeleteReview(db.DefaultContext, review2))

	_, err = issues_model.GetReviewByID(db.DefaultContext, review2.ID)
	require.Error(t, err)
	assert.True(t, issues_model.IsErrReviewNotExist(err), "IsErrReviewNotExist")

	review1, err = issues_model.GetReviewByID(db.DefaultContext, review1.ID)
	require.NoError(t, err)
	assert.True(t, review1.Official)
}

func TestDeleteDismissedReview(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	review, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Content:  "reject",
		Type:     issues_model.ReviewTypeReject,
		Official: false,
		Issue:    issue,
		Reviewer: user,
	})
	require.NoError(t, err)
	require.NoError(t, issues_model.DismissReview(db.DefaultContext, review, true))
	comment, err := issues_model.CreateComment(db.DefaultContext, &issues_model.CreateCommentOptions{
		Type:     issues_model.CommentTypeDismissReview,
		Doer:     user,
		Repo:     repo,
		Issue:    issue,
		ReviewID: review.ID,
		Content:  "dismiss",
	})
	require.NoError(t, err)
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: comment.ID})
	require.NoError(t, issues_model.DeleteReview(db.DefaultContext, review))
	unittest.AssertNotExistsBean(t, &issues_model.Comment{ID: comment.ID})
}

func TestAddReviewRequest(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pull := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 1})
	require.NoError(t, pull.LoadIssue(db.DefaultContext))
	issue := pull.Issue
	require.NoError(t, issue.LoadRepo(db.DefaultContext))
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	_, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Issue:    issue,
		Reviewer: reviewer,
		Type:     issues_model.ReviewTypeReject,
	})

	require.NoError(t, err)
	pull.HasMerged = false
	require.NoError(t, pull.UpdateCols(db.DefaultContext, "has_merged"))
	issue.IsClosed = true
	_, err = issues_model.AddReviewRequest(db.DefaultContext, issue, reviewer, &user_model.User{})
	require.Error(t, err)
	assert.True(t, issues_model.IsErrReviewRequestOnClosedPR(err))

	pull.HasMerged = true
	require.NoError(t, pull.UpdateCols(db.DefaultContext, "has_merged"))
	issue.IsClosed = false
	_, err = issues_model.AddReviewRequest(db.DefaultContext, issue, reviewer, &user_model.User{})
	require.Error(t, err)
	assert.True(t, issues_model.IsErrReviewRequestOnClosedPR(err))
}

func TestSubmitPendingReviewDeletesReviewRequest(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	pull := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 1})
	require.NoError(t, pull.LoadIssue(db.DefaultContext))
	issue := pull.Issue
	require.NoError(t, issue.LoadRepo(db.DefaultContext))
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	reviewRequest, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Issue:    issue,
		Reviewer: reviewer,
		Type:     issues_model.ReviewTypeRequest,
	})
	require.NoError(t, err)

	// creating a pending review should NOT remove review requests
	reviewPending, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Issue:    issue,
		Reviewer: reviewer,
		Type:     issues_model.ReviewTypePending,
	})
	require.NoError(t, err)
	unittest.AssertExistsIf(t, true, &issues_model.Review{ID: reviewRequest.ID})
	// submitting a pending review to finish it SHOULD remove review requests
	_, _, err = issues_model.SubmitReview(
		db.DefaultContext,
		reviewer,
		issue,
		issues_model.ReviewTypeReject,
		"test content",
		reviewPending.CommitID,
		false,
		[]string{},
	)
	require.NoError(t, err)
	unittest.AssertNotExistsBean(t, &issues_model.Review{ID: reviewRequest.ID})
}

// this test is for handling a state correctly that should never exist, but is representable and was
// achievable thanks to #12243
func TestReviewRequestDeletesReviewRequestsBeforeRejectedReviews(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	sess := db.GetEngine(db.DefaultContext)

	pull := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 1})
	require.NoError(t, pull.LoadIssue(db.DefaultContext))
	issue := pull.Issue
	require.NoError(t, issue.LoadRepo(db.DefaultContext))
	reviewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

	// this one will end up being a ReviewTypeRequest. We are initially creating it as
	// ReviewTypeReject to avoid it being deleted on making the actual rejected review
	reviewRequest, err := issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Issue:    issue,
		Reviewer: reviewer,
		Type:     issues_model.ReviewTypeReject,
	})
	require.NoError(t, err)
	// this review is an actual rejected review that somehow managed to be saved without deleting
	// reviewRequest. This is a state that is representable and is/was achievable thanks to #12243
	_, err = issues_model.CreateReview(db.DefaultContext, issues_model.CreateReviewOptions{
		Issue:    issue,
		Reviewer: reviewer,
		Type:     issues_model.ReviewTypeReject,
	})
	require.NoError(t, err)
	reviewRequest.Type = issues_model.ReviewTypeRequest
	_, err = sess.ID(reviewRequest.ID).Cols("type").Update(reviewRequest)
	require.NoError(t, err)

	_, err = issues_model.RemoveReviewRequest(db.DefaultContext, issue, reviewer, doer)
	require.NoError(t, err)
	unittest.AssertNotExistsBean(t, &issues_model.Review{ID: reviewRequest.ID})
}

func TestAddTeamReviewRequest(t *testing.T) {
	defer unittest.OverrideFixtures("models/fixtures/TestAddTeamReviewRequest")()
	require.NoError(t, unittest.PrepareTestDatabase())

	setupForProtectedBranch := func() (*issues_model.Issue, *user_model.User) {
		// From override models/fixtures/TestAddTeamReviewRequest/issue.yml; issue #23 is a PR into a protected branch
		issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 23})
		require.NoError(t, issue.LoadRepo(db.DefaultContext))
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		return issue, doer
	}

	t.Run("Protected branch, not official team", func(t *testing.T) {
		issue, doer := setupForProtectedBranch()
		// Team 2 is not part of the whitelist for this protected branch
		team := unittest.AssertExistsAndLoadBean(t, &org_model.Team{ID: 2})

		comment, err := issues_model.AddTeamReviewRequest(db.DefaultContext, issue, team, doer)
		require.NoError(t, err)
		require.NotNil(t, comment)

		review, err := issues_model.GetTeamReviewerByIssueIDAndTeamID(db.DefaultContext, issue.ID, team.ID)
		require.NoError(t, err)
		require.NotNil(t, review)
		assert.Equal(t, issues_model.ReviewTypeRequest, review.Type)
		assert.Equal(t, team.ID, review.ReviewerTeamID)
		// This review request should not be marked official because it is not a request for a team in the branch
		// protection rule's whitelist...
		assert.False(t, review.Official)
	})

	t.Run("Protected branch, official team", func(t *testing.T) {
		issue, doer := setupForProtectedBranch()
		// Team 1 is part of the whitelist for this protected branch
		team := unittest.AssertExistsAndLoadBean(t, &org_model.Team{ID: 1})

		comment, err := issues_model.AddTeamReviewRequest(db.DefaultContext, issue, team, doer)
		require.NoError(t, err)
		require.NotNil(t, comment)

		review, err := issues_model.GetTeamReviewerByIssueIDAndTeamID(db.DefaultContext, issue.ID, team.ID)
		require.NoError(t, err)
		require.NotNil(t, review)
		assert.Equal(t, issues_model.ReviewTypeRequest, review.Type)
		assert.Equal(t, team.ID, review.ReviewerTeamID)
		// Expected to be considered official because team 1 is in the review whitelist for this protected branch
		assert.True(t, review.Official)
	})

	t.Run("Unprotected branch, official team", func(t *testing.T) {
		// Working on a PR into a branch that is not protected, issue #2
		issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 2})
		require.NoError(t, issue.LoadRepo(db.DefaultContext))
		doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
		// team is a team that has write perms against the repo
		team := unittest.AssertExistsAndLoadBean(t, &org_model.Team{ID: 1})

		comment, err := issues_model.AddTeamReviewRequest(db.DefaultContext, issue, team, doer)
		require.NoError(t, err)
		require.NotNil(t, comment)

		review, err := issues_model.GetTeamReviewerByIssueIDAndTeamID(db.DefaultContext, issue.ID, team.ID)
		require.NoError(t, err)
		require.NotNil(t, review)
		assert.Equal(t, issues_model.ReviewTypeRequest, review.Type)
		assert.Equal(t, team.ID, review.ReviewerTeamID)
		// Will not be marked as official because PR #2 there's no branch protection rule that enables whitelist
		// approvals (verifying logic in `IsOfficialReviewerTeam` indirectly)
		assert.False(t, review.Official)

		// Adding the same team review request again should be a noop
		comment, err = issues_model.AddTeamReviewRequest(db.DefaultContext, issue, team, doer)
		require.NoError(t, err)
		require.Nil(t, comment)
	})
}
