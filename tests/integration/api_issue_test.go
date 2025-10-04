// Copyright 2017 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm/convert"
)

func TestAPIListIssues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo.OwnerID})

	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue)
	link, _ := url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/issues", owner.Name, repo.Name))

	link.RawQuery = url.Values{"token": {token}, "state": {"all"}}.Encode()
	resp := MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
	var apiIssues []*api.Issue
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, unittest.GetCount(t, &issues_model.Issue{RepoID: repo.ID}))
	for _, apiIssue := range apiIssues {
		unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: apiIssue.ID, RepoID: repo.ID})
	}

	// test milestone filter
	link.RawQuery = url.Values{"token": {token}, "state": {"all"}, "type": {"all"}, "milestones": {"ignore,milestone1,3,4"}}.Encode()
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	if assert.Len(t, apiIssues, 2) {
		assert.EqualValues(t, 3, apiIssues[0].Milestone.ID)
		assert.EqualValues(t, 1, apiIssues[1].Milestone.ID)
	}

	link.RawQuery = url.Values{"token": {token}, "state": {"all"}, "created_by": {"user2"}}.Encode()
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	if assert.Len(t, apiIssues, 1) {
		assert.EqualValues(t, 5, apiIssues[0].ID)
	}

	link.RawQuery = url.Values{"token": {token}, "state": {"all"}, "assigned_by": {"user1"}}.Encode()
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	if assert.Len(t, apiIssues, 1) {
		assert.EqualValues(t, 1, apiIssues[0].ID)
	}

	link.RawQuery = url.Values{"token": {token}, "state": {"all"}, "mentioned_by": {"user4"}}.Encode()
	resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	if assert.Len(t, apiIssues, 1) {
		assert.EqualValues(t, 1, apiIssues[0].ID)
	}

	t.Run("Sort", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		link.RawQuery = url.Values{"token": {token}, "sort": {"oldest"}}.Encode()
		resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
		DecodeJSON(t, resp, &apiIssues)
		if assert.Len(t, apiIssues, 4) {
			assert.EqualValues(t, 1, apiIssues[0].ID)
			assert.EqualValues(t, 2, apiIssues[1].ID)
			assert.EqualValues(t, 3, apiIssues[2].ID)
			assert.EqualValues(t, 11, apiIssues[3].ID)
		}

		link.RawQuery = url.Values{"token": {token}, "sort": {"newest"}}.Encode()
		resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
		DecodeJSON(t, resp, &apiIssues)
		if assert.Len(t, apiIssues, 4) {
			assert.EqualValues(t, 11, apiIssues[0].ID)
			assert.EqualValues(t, 3, apiIssues[1].ID)
			assert.EqualValues(t, 2, apiIssues[2].ID)
			assert.EqualValues(t, 1, apiIssues[3].ID)
		}

		link.RawQuery = url.Values{"token": {token}, "sort": {"recentupdate"}}.Encode()
		resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
		DecodeJSON(t, resp, &apiIssues)
		if assert.Len(t, apiIssues, 4) {
			assert.EqualValues(t, 11, apiIssues[0].ID)
			assert.EqualValues(t, 1, apiIssues[1].ID)
			assert.EqualValues(t, 2, apiIssues[2].ID)
			assert.EqualValues(t, 3, apiIssues[3].ID)
		}

		link.RawQuery = url.Values{"token": {token}, "sort": {"leastupdate"}}.Encode()
		resp = MakeRequest(t, NewRequest(t, "GET", link.String()), http.StatusOK)
		DecodeJSON(t, resp, &apiIssues)
		if assert.Len(t, apiIssues, 4) {
			assert.EqualValues(t, 3, apiIssues[0].ID)
			assert.EqualValues(t, 2, apiIssues[1].ID)
			assert.EqualValues(t, 1, apiIssues[2].ID)
			assert.EqualValues(t, 11, apiIssues[3].ID)
		}
	})
}

func TestAPIListIssuesPublicOnly(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo1.OwnerID})

	session := loginUser(t, owner1.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue)
	link, _ := url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/issues", owner1.Name, repo1.Name))
	link.RawQuery = url.Values{"state": {"all"}}.Encode()
	req := NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusOK)

	repo2 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 2})
	owner2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repo2.OwnerID})

	session = loginUser(t, owner2.Name)
	token = getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue)
	link, _ = url.Parse(fmt.Sprintf("/api/v1/repos/%s/%s/issues", owner2.Name, repo2.Name))
	link.RawQuery = url.Values{"state": {"all"}}.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusOK)

	publicOnlyToken := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeReadIssue, auth_model.AccessTokenScopePublicOnly)
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(publicOnlyToken)
	MakeRequest(t, req, http.StatusForbidden)
}

func TestAPICreateIssue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const body, title = "apiTestBody", "apiTestTitle"

	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repoBefore.OwnerID})

	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues?state=all", owner.Name, repoBefore.Name)
	req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateIssueOption{
		Body:     body,
		Title:    title,
		Assignee: owner.Name,
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)
	var apiIssue api.Issue
	DecodeJSON(t, resp, &apiIssue)
	assert.Equal(t, body, apiIssue.Body)
	assert.Equal(t, title, apiIssue.Title)

	unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{
		RepoID:     repoBefore.ID,
		AssigneeID: owner.ID,
		Content:    body,
		Title:      title,
	})

	repoAfter := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	assert.Equal(t, repoBefore.NumIssues+1, repoAfter.NumIssues)
	assert.Equal(t, repoBefore.NumClosedIssues, repoAfter.NumClosedIssues)
}

func TestAPICreateIssueParallel(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	const body, title = "apiTestBody", "apiTestTitle"

	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repoBefore.OwnerID})

	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues?state=all", owner.Name, repoBefore.Name)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(parentT *testing.T, i int) {
			parentT.Run(fmt.Sprintf("ParallelCreateIssue_%d", i), func(t *testing.T) {
				newTitle := title + strconv.Itoa(i)
				newBody := body + strconv.Itoa(i)
				req := NewRequestWithJSON(t, "POST", urlStr, &api.CreateIssueOption{
					Body:     newBody,
					Title:    newTitle,
					Assignee: owner.Name,
				}).AddTokenAuth(token)
				resp := MakeRequest(t, req, http.StatusCreated)
				var apiIssue api.Issue
				DecodeJSON(t, resp, &apiIssue)
				assert.Equal(t, newBody, apiIssue.Body)
				assert.Equal(t, newTitle, apiIssue.Title)

				unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{
					RepoID:     repoBefore.ID,
					AssigneeID: owner.ID,
					Content:    newBody,
					Title:      newTitle,
				})

				wg.Done()
			})
		}(t, i)
	}
	wg.Wait()
}

func TestAPIEditIssue(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 10})
	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issueBefore.RepoID})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repoBefore.OwnerID})
	require.NoError(t, issueBefore.LoadAttributes(db.DefaultContext))
	assert.Equal(t, int64(1019307200), int64(issueBefore.DeadlineUnix))
	assert.Equal(t, api.StateOpen, issueBefore.State())

	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)

	// update values of issue
	issueState := "closed"
	removeDeadline := true
	milestone := int64(4)
	body := "new content!"
	title := "new title from api set"

	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner.Name, repoBefore.Name, issueBefore.Index)
	req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
		State:          &issueState,
		RemoveDeadline: &removeDeadline,
		Milestone:      &milestone,
		Body:           &body,
		Title:          title,

		// ToDo change more
	}).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusCreated)
	var apiIssue api.Issue
	DecodeJSON(t, resp, &apiIssue)

	issueAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 10})
	repoAfter := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issueBefore.RepoID})

	// check comment history
	unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: issueAfter.ID, OldTitle: issueBefore.Title, NewTitle: title})
	unittest.AssertExistsAndLoadBean(t, &issues_model.ContentHistory{IssueID: issueAfter.ID, ContentText: body, IsFirstCreated: false})

	// check deleted user
	assert.Equal(t, int64(500), issueAfter.PosterID)
	require.NoError(t, issueAfter.LoadAttributes(db.DefaultContext))
	assert.Equal(t, int64(-1), issueAfter.PosterID)
	assert.Equal(t, int64(-1), issueBefore.PosterID)
	assert.Equal(t, int64(-1), apiIssue.Poster.ID)

	// check repo change
	assert.Equal(t, repoBefore.NumClosedIssues+1, repoAfter.NumClosedIssues)

	// API response
	assert.Equal(t, api.StateClosed, apiIssue.State)
	assert.Equal(t, milestone, apiIssue.Milestone.ID)
	assert.Equal(t, body, apiIssue.Body)
	assert.Nil(t, apiIssue.Deadline)
	assert.Equal(t, title, apiIssue.Title)

	// in database
	assert.Equal(t, api.StateClosed, issueAfter.State())
	assert.Equal(t, milestone, issueAfter.MilestoneID)
	assert.Equal(t, int64(0), int64(issueAfter.DeadlineUnix))
	assert.Equal(t, body, issueAfter.Content)
	assert.Equal(t, title, issueAfter.Title)

	// verify the idempotency of state, milestone, body and title changes
	req = NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
		State:     &issueState,
		Milestone: &milestone,
		Body:      &body,
		Title:     title,
	}).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusCreated)
	var apiIssueIdempotent api.Issue
	DecodeJSON(t, resp, &apiIssueIdempotent)
	assert.Equal(t, apiIssue.State, apiIssueIdempotent.State)
	assert.Equal(t, apiIssue.Milestone.Title, apiIssueIdempotent.Milestone.Title)
	assert.Equal(t, apiIssue.Body, apiIssueIdempotent.Body)
	assert.Equal(t, apiIssue.Title, apiIssueIdempotent.Title)
}

func TestAPIEditIssueAutoDate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 13})
	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issueBefore.RepoID})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repoBefore.OwnerID})
	require.NoError(t, issueBefore.LoadAttributes(db.DefaultContext))

	t.Run("WithAutoDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// User2 is not owner, but can update the 'public' issue with auto date
		session := loginUser(t, "user2")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner.Name, repoBefore.Name, issueBefore.Index)

		body := "new content!"
		req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
			Body: &body,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var apiIssue api.Issue
		DecodeJSON(t, resp, &apiIssue)

		// the execution of the API call supposedly lasted less than one minute
		updatedSince := time.Since(apiIssue.Updated)
		assert.LessOrEqual(t, updatedSince, time.Minute)

		issueAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issueBefore.ID})
		updatedSince = time.Since(issueAfter.UpdatedUnix.AsTime())
		assert.LessOrEqual(t, updatedSince, time.Minute)
	})

	t.Run("WithUpdateDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// User1 is admin, and so can update the issue without auto date
		session := loginUser(t, "user1")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner.Name, repoBefore.Name, issueBefore.Index)

		body := "new content, with updated time"
		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
		req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
			Body:    &body,
			Updated: &updatedAt,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusCreated)
		var apiIssue api.Issue
		DecodeJSON(t, resp, &apiIssue)

		// dates are converted into the same tz, in order to compare them
		utcTZ, _ := time.LoadLocation("UTC")
		assert.Equal(t, updatedAt.In(utcTZ), apiIssue.Updated.In(utcTZ))

		issueAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issueBefore.ID})
		assert.Equal(t, updatedAt.In(utcTZ), issueAfter.UpdatedUnix.AsTime().In(utcTZ))
	})

	t.Run("WithoutPermission", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// User2 is not owner nor admin, and so can't update the issue without auto date
		session := loginUser(t, "user2")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)
		urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner.Name, repoBefore.Name, issueBefore.Index)

		body := "new content, with updated time"
		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
		req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
			Body:    &body,
			Updated: &updatedAt,
		}).AddTokenAuth(token)
		resp := MakeRequest(t, req, http.StatusForbidden)
		var apiError api.APIError
		DecodeJSON(t, resp, &apiError)

		assert.Equal(t, "user needs to have admin or owner right", apiError.Message)
	})
}

func TestAPIEditIssueMilestoneAutoDate(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issueBefore.RepoID})

	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: repoBefore.OwnerID})
	require.NoError(t, issueBefore.LoadAttributes(db.DefaultContext))

	session := loginUser(t, owner.Name)
	token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteIssue)
	urlStr := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner.Name, repoBefore.Name, issueBefore.Index)

	t.Run("WithAutoDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		milestone := int64(1)
		req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
			Milestone: &milestone,
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		// the execution of the API call supposedly lasted less than one minute
		milestoneAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Milestone{ID: milestone})
		updatedSince := time.Since(milestoneAfter.UpdatedUnix.AsTime())
		assert.LessOrEqual(t, updatedSince, time.Minute)
	})

	t.Run("WithPostUpdateDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Note: the updated_unix field of the test Milestones is set to NULL
		// Hence, any date is higher than the Milestone's updated date
		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
		milestone := int64(2)
		req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
			Milestone: &milestone,
			Updated:   &updatedAt,
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		// the milestone date should be set to 'updatedAt'
		// dates are converted into the same tz, in order to compare them
		utcTZ, _ := time.LoadLocation("UTC")
		milestoneAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Milestone{ID: milestone})
		assert.Equal(t, updatedAt.In(utcTZ), milestoneAfter.UpdatedUnix.AsTime().In(utcTZ))
	})

	t.Run("WithPastUpdateDate", func(t *testing.T) {
		defer tests.PrintCurrentTest(t)()

		// Note: This Milestone's updated_unix has been set to Now() by the first subtest
		milestone := int64(1)
		milestoneBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Milestone{ID: milestone})

		updatedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
		req := NewRequestWithJSON(t, "PATCH", urlStr, api.EditIssueOption{
			Milestone: &milestone,
			Updated:   &updatedAt,
		}).AddTokenAuth(token)
		MakeRequest(t, req, http.StatusCreated)

		// the milestone date should not change
		// dates are converted into the same tz, in order to compare them
		utcTZ, _ := time.LoadLocation("UTC")
		milestoneAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Milestone{ID: milestone})
		assert.Equal(t, milestoneAfter.UpdatedUnix.AsTime().In(utcTZ), milestoneBefore.UpdatedUnix.AsTime().In(utcTZ))
	})
}

func TestAPISearchIssues(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// as this API was used in the frontend, it uses UI page size
	expectedIssueCount := 20 // from the fixtures
	if expectedIssueCount > setting.UI.IssuePagingNum {
		expectedIssueCount = setting.UI.IssuePagingNum
	}

	link, _ := url.Parse("/api/v1/repos/issues/search")
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeReadIssue)
	query := url.Values{}
	var apiIssues []*api.Issue

	link.RawQuery = query.Encode()
	req := NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, expectedIssueCount)

	publicOnlyToken := getUserToken(t, "user1", auth_model.AccessTokenScopeReadIssue, auth_model.AccessTokenScopePublicOnly)
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(publicOnlyToken)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 15) // 15 public issues

	since := "2000-01-01T00:50:01+00:00" // 946687801
	before := time.Unix(999307200, 0).Format(time.RFC3339)
	query.Add("since", since)
	query.Add("before", before)
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 11)
	query.Del("since")
	query.Del("before")

	query.Add("state", "closed")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	query.Set("state", "all")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Equal(t, "22", resp.Header().Get("X-Total-Count"))
	assert.Len(t, apiIssues, 20)

	query.Add("limit", "10")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Equal(t, "22", resp.Header().Get("X-Total-Count"))
	assert.Len(t, apiIssues, 10)

	query = url.Values{"assigned": {"true"}, "state": {"all"}}
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	query = url.Values{"milestones": {"milestone1"}, "state": {"all"}}
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 1)

	query = url.Values{"milestones": {"milestone1,milestone3"}, "state": {"all"}}
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	query = url.Values{"owner": {"user2"}} // user
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 8)

	query = url.Values{"owner": {"org3"}} // organization
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 5)

	query = url.Values{"owner": {"org3"}, "team": {"team1"}} // organization + team
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)
}

func TestAPISearchIssuesWithLabels(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	// as this API was used in the frontend, it uses UI page size
	expectedIssueCount := 20 // from the fixtures
	if expectedIssueCount > setting.UI.IssuePagingNum {
		expectedIssueCount = setting.UI.IssuePagingNum
	}

	link, _ := url.Parse("/api/v1/repos/issues/search")
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeReadIssue)
	query := url.Values{}
	var apiIssues []*api.Issue

	link.RawQuery = query.Encode()
	req := NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp := MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, expectedIssueCount)

	query.Add("labels", "label1")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	// multiple labels
	query.Set("labels", "label1,label2")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	// an org label
	query.Set("labels", "orglabel4")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 1)

	// org and repo label
	query.Set("labels", "label2,orglabel4")
	query.Add("state", "all")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)

	// org and repo label which share the same issue
	query.Set("labels", "label1,orglabel4")
	link.RawQuery = query.Encode()
	req = NewRequest(t, "GET", link.String()).AddTokenAuth(token)
	resp = MakeRequest(t, req, http.StatusOK)
	DecodeJSON(t, resp, &apiIssues)
	assert.Len(t, apiIssues, 2)
}

func TestAPIInternalAndExternalIssueTracker(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	otherUser := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	token := getUserToken(t, user.Name, auth_model.AccessTokenScopeAll)

	internalIssueRepo, _, reset := tests.CreateDeclarativeRepoWithOptions(t, user, tests.DeclarativeRepoOptions{
		Name:          optional.Some("internal-issues"),
		EnabledUnits:  optional.Some([]unit.Type{unit.TypeIssues}),
		DisabledUnits: optional.Some([]unit.Type{unit.TypeExternalTracker}),
		UnitConfig: optional.Some(map[unit.Type]convert.Conversion{
			unit.TypeIssues: &repo_model.IssuesConfig{
				EnableTimetracker:  true,
				EnableDependencies: true,
			},
		}),
	})
	defer reset()

	externalIssueRepo, _, reset := tests.CreateDeclarativeRepoWithOptions(t, user, tests.DeclarativeRepoOptions{
		Name:          optional.Some("external-issues"),
		EnabledUnits:  optional.Some([]unit.Type{unit.TypeExternalTracker}),
		DisabledUnits: optional.Some([]unit.Type{unit.TypeIssues}),
	})
	defer reset()

	disabledIssueRepo, _, reset := tests.CreateDeclarativeRepoWithOptions(t, user, tests.DeclarativeRepoOptions{
		Name:          optional.Some("disabled-issues"),
		DisabledUnits: optional.Some([]unit.Type{unit.TypeIssues, unit.TypeExternalTracker}),
	})
	defer reset()

	runTest := func(t *testing.T, repo *repo_model.Repository, requestAllowed bool) {
		t.Helper()
		getPath := func(path string, args ...any) string {
			suffix := path
			if len(args) > 0 {
				suffix = fmt.Sprintf(path, args...)
			}
			return fmt.Sprintf("/api/v1/repos/%s/%s/issues%s", repo.OwnerName, repo.Name, suffix)
		}
		getStatus := func(allowStatus int) int {
			if requestAllowed {
				return allowStatus
			}
			return http.StatusNotFound
		}
		okStatus := getStatus(http.StatusOK)
		createdStatus := getStatus(http.StatusCreated)
		noContentStatus := getStatus(http.StatusNoContent)

		// setup
		issue := createIssue(t, user, repo, "normal issue", uuid.NewString())
		deleteIssue := createIssue(t, user, repo, "delete this issue", uuid.NewString())
		dependencyIssue := createIssue(t, user, repo, "depend on this issue", uuid.NewString())
		blocksIssue := createIssue(t, user, repo, "depend on this issue", uuid.NewString())

		// issues
		MakeRequest(t, NewRequest(t, "GET", getPath("/")).AddTokenAuth(token), http.StatusOK)
		MakeRequest(t, NewRequestWithValues(t, "POST", getPath("/"), map[string]string{"title": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d", issue.Index)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithValues(t, "PATCH", getPath("/%d", deleteIssue.Index), map[string]string{"title": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d", deleteIssue.Index)).AddTokenAuth(token), noContentStatus)

		MakeRequest(t, NewRequest(t, "GET", getPath("/pinned")).AddTokenAuth(token), okStatus)

		// comments
		MakeRequest(t, NewRequest(t, "GET", getPath("/comments")).AddTokenAuth(token), http.StatusOK)
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/comments", issue.Index)).AddTokenAuth(token), okStatus)
		resp := MakeRequest(t, NewRequestWithValues(t, "POST", getPath("/%d/comments", issue.Index), map[string]string{"body": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		var comment api.Comment
		DecodeJSON(t, resp, &comment)
		resp = MakeRequest(t, NewRequestWithValues(t, "POST", getPath("/%d/comments", issue.Index), map[string]string{"body": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		var commentTwo api.Comment
		DecodeJSON(t, resp, &commentTwo)
		resp = MakeRequest(t, NewRequestWithValues(t, "POST", getPath("/%d/comments", issue.Index), map[string]string{"body": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		var commentThree api.Comment
		DecodeJSON(t, resp, &commentThree)
		MakeRequest(t, NewRequest(t, "GET", getPath("/comments/%d", commentTwo.ID)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithValues(t, "PATCH", getPath("/comments/%d", commentTwo.ID), map[string]string{"body": uuid.NewString()}).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/comments/%d", commentTwo.ID)).AddTokenAuth(token), noContentStatus)
		MakeRequest(t, NewRequestWithValues(t, "PATCH", getPath("/%d/comments/%d", issue.Index, commentThree.ID), map[string]string{"body": uuid.NewString()}).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/comments/%d", issue.Index, commentThree.ID)).AddTokenAuth(token), noContentStatus)
		// comment-reactions
		MakeRequest(t, NewRequest(t, "GET", getPath("/comments/%d/reactions", comment.ID)).AddTokenAuth(token), okStatus)
		reaction := &api.EditReactionOption{Reaction: "+1"}
		MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/comments/%d/reactions", comment.ID), reaction).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequestWithJSON(t, "DELETE", getPath("/comments/%d/reactions", comment.ID), reaction).AddTokenAuth(token), okStatus)
		// comment-assets
		MakeRequest(t, NewRequest(t, "GET", getPath("/comments/%d/assets", comment.ID)).AddTokenAuth(token), okStatus)
		body := &bytes.Buffer{}
		contentType := tests.WriteImageBody(t, generateImg(), "image.png", body)
		req := NewRequestWithBody(t, "POST", getPath("/comments/%d/assets", comment.ID), bytes.NewReader(body.Bytes())).AddTokenAuth(token)
		req.Header.Add("Content-Type", contentType)
		resp = MakeRequest(t, req, createdStatus)
		var commentAttachment api.Attachment
		DecodeJSON(t, resp, &commentAttachment)
		MakeRequest(t, NewRequest(t, "GET", getPath("/comments/%d/assets/%d", comment.ID, commentAttachment.ID)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithValues(t, "PATCH", getPath("/comments/%d/assets/%d", comment.ID, commentAttachment.ID), map[string]string{"name": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/comments/%d/assets/%d", comment.ID, commentAttachment.ID)).AddTokenAuth(token), noContentStatus)

		// timeline
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/timeline", issue.Index)).AddTokenAuth(token), okStatus)

		// labels
		labelName := uuid.NewString()
		labelCreateURL := fmt.Sprintf("/api/v1/repos/%s/%s/labels", repo.OwnerName, repo.Name)
		resp = MakeRequest(t, NewRequestWithValues(t, "POST", labelCreateURL, map[string]string{"name": labelName, "color": "#333333"}).AddTokenAuth(token), http.StatusCreated)
		var label api.Label
		DecodeJSON(t, resp, &label)

		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/labels", issue.Index)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/labels", issue.Index), api.IssueLabelsOption{Labels: []any{labelName}}).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithJSON(t, "PUT", getPath("/%d/labels", issue.Index), api.IssueLabelsOption{Labels: []any{labelName}}).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/labels", issue.Index)).AddTokenAuth(token), noContentStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/labels/%d", issue.Index, label.ID)).AddTokenAuth(token), noContentStatus)

		// times
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/times", issue.Index)).AddTokenAuth(token), okStatus)
		resp = MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/times", issue.Index), api.AddTimeOption{Time: 60}).AddTokenAuth(token), okStatus)
		var trackedTime api.TrackedTime
		DecodeJSON(t, resp, &trackedTime)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/times", issue.Index)).AddTokenAuth(token), noContentStatus)
		resp = MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/times", issue.Index), api.AddTimeOption{Time: 75}).AddTokenAuth(token), okStatus)
		DecodeJSON(t, resp, &trackedTime)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/times/%d", issue.Index, trackedTime.ID)).AddTokenAuth(token), noContentStatus)

		// deadline
		MakeRequest(t, NewRequestWithValues(t, "POST", getPath("/%d/deadline", issue.Index), map[string]string{"due_date": "2022-04-06T00:00:00.000Z"}).AddTokenAuth(token), createdStatus)

		// stopwatch
		MakeRequest(t, NewRequest(t, "POST", getPath("/%d/stopwatch/start", issue.Index)).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "POST", getPath("/%d/stopwatch/stop", issue.Index)).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "POST", getPath("/%d/stopwatch/start", issue.Index)).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/stopwatch/delete", issue.Index)).AddTokenAuth(token), noContentStatus)

		// subscriptions
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/subscriptions", issue.Index)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/subscriptions/check", issue.Index)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequest(t, "PUT", getPath("/%d/subscriptions/%s", issue.Index, otherUser.Name)).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/subscriptions/%s", issue.Index, otherUser.Name)).AddTokenAuth(token), createdStatus)

		// reactions
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/reactions", issue.Index)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/reactions", issue.Index), api.EditReactionOption{Reaction: "+1"}).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequestWithJSON(t, "DELETE", getPath("/%d/reactions", issue.Index), api.EditReactionOption{Reaction: "+1"}).AddTokenAuth(token), okStatus)

		// assets
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/assets", issue.Index)).AddTokenAuth(token), okStatus)
		req = NewRequestWithBody(t, "POST", getPath("/%d/assets", issue.Index), bytes.NewReader(body.Bytes())).AddTokenAuth(token)
		req.Header.Add("Content-Type", contentType)
		resp = MakeRequest(t, req, createdStatus)
		var attachment api.Attachment
		DecodeJSON(t, resp, &attachment)
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/assets/%d", issue.Index, attachment.ID)).AddTokenAuth(token), okStatus)
		MakeRequest(t, NewRequestWithValues(t, "PATCH", getPath("/%d/assets/%d", issue.Index, attachment.ID), map[string]string{"name": uuid.NewString()}).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequest(t, "DELETE", getPath("/%d/assets/%d", issue.Index, attachment.ID)).AddTokenAuth(token), noContentStatus)

		// dependencies
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/dependencies", issue.Index)).AddTokenAuth(token), okStatus)
		dependencyMeta := api.IssueMeta{Index: dependencyIssue.Index, Owner: dependencyIssue.Repo.OwnerName, Name: dependencyIssue.Repo.Name}
		MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/dependencies", issue.Index), dependencyMeta).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequestWithJSON(t, "DELETE", getPath("/%d/dependencies", issue.Index), dependencyMeta).AddTokenAuth(token), createdStatus)

		// blocks
		MakeRequest(t, NewRequest(t, "GET", getPath("/%d/blocks", issue.Index)).AddTokenAuth(token), okStatus)
		blockMeta := api.IssueMeta{Index: blocksIssue.Index, Owner: blocksIssue.Repo.OwnerName, Name: blocksIssue.Repo.Name}
		MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/blocks", issue.Index), blockMeta).AddTokenAuth(token), createdStatus)
		MakeRequest(t, NewRequestWithJSON(t, "DELETE", getPath("/%d/blocks", issue.Index), blockMeta).AddTokenAuth(token), createdStatus)

		// pin
		MakeRequest(t, NewRequestWithJSON(t, "POST", getPath("/%d/pin", issue.Index), blockMeta).AddTokenAuth(token), noContentStatus)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", getPath("/%d/pin/1", issue.Index), blockMeta).AddTokenAuth(token), noContentStatus)
		MakeRequest(t, NewRequestWithJSON(t, "DELETE", getPath("/%d/pin", issue.Index), blockMeta).AddTokenAuth(token), noContentStatus)
	}

	runTest(t, internalIssueRepo, true)
	runTest(t, externalIssueRepo, false)
	runTest(t, disabledIssueRepo, false)
}
