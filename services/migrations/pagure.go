// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"forgejo.org/modules/json"
	"forgejo.org/modules/log"
	base "forgejo.org/modules/migration"
	"forgejo.org/modules/proxy"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/util"
)

var (
	_ base.Downloader        = &PagureDownloader{}
	_ base.DownloaderFactory = &PagureDownloaderFactory{}
)

func init() {
	RegisterDownloaderFactory(&PagureDownloaderFactory{})
}

// PagureDownloaderFactory defines a downloader factory
type PagureDownloaderFactory struct{}

// PagureUser defines a user on Pagure to be migrated over to Forgejo
type PagureUser struct {
	FullURL  string `json:"full_url"`
	Fullname string `json:"fullname"`
	Name     string `json:"name"`
	URLPath  string `json:"url_path"`
}

// PagureRepoInfo describes the repository with preliminary information
type PagureRepoInfo struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	FullName      string            `json:"fullname"`
	Description   string            `json:"description"`
	Topics        []string          `json:"tags"`
	CloseStatuses []string          `json:"close_status"`
	Priorities    map[string]string `json:"priorities"`
	Milestones    map[string]struct {
		Active bool    `json:"active"`
		Date   *string `json:"date"`
	} `json:"milestones"`
}

// PagureDownloader implements a Downloader interface to get repository information from Pagure
type PagureDownloader struct {
	base.NullDownloader
	ctx                   context.Context
	client                *http.Client
	baseURL               *url.URL
	meta                  PagureRepoInfo
	repoName              string
	token                 string
	privateIssuesOnlyRepo bool
	repoID                int64
	maxIssueIndex         int64
	userMap               map[string]*PagureUser
	milestoneMap          map[int64]string
	priorities            map[string]string
}

// PagureLabelsList defines a list of labels under an issue tracker
type PagureLabelsList struct {
	Labels []string `json:"tags"`
}

// PagureLabel defines a label under the issue tracker labels list
type PagureLabel struct {
	Label            string `json:"tag"`
	LabelColor       string `json:"tag_color"`
	LabelDescription string `json:"tag_description"`
}

// PagureIssueContext confirms if a said unit is an issue ticket or a pull request
type PagureIssueContext struct {
	IsPullRequest bool
}

// PagureIssuesResponse describes a list of issue tickets under an issue tracker
type PagureIssuesResponse struct {
	Issues []struct {
		Assignee     any        `json:"assignee"`
		Blocks       []string   `json:"blocks"`
		CloseStatus  string     `json:"close_status"`
		ClosedAt     string     `json:"closed_at"`
		ClosedBy     any        `json:"closed_by"`
		Comments     []any      `json:"comments"`
		Content      string     `json:"content"`
		CustomFields []any      `json:"custom_fields"`
		DateCreated  string     `json:"date_created"`
		Depends      []any      `json:"depends"`
		ID           int64      `json:"id"`
		LastUpdated  string     `json:"last_updated"`
		Milestone    string     `json:"milestone"`
		Priority     int64      `json:"priority"`
		Private      bool       `json:"private"`
		Status       string     `json:"status"`
		Tags         []string   `json:"tags"`
		Title        string     `json:"title"`
		User         PagureUser `json:"user"`
	} `json:"issues"`
	Pagination struct {
		First   string  `json:"first"`
		Last    string  `json:"last"`
		Next    *string `json:"next"`
		Page    int     `json:"page"`
		Pages   int     `json:"pages"`
		PerPage int     `json:"per_page"`
		Prev    string  `json:"prev"`
	}
}

// PagureIssueDetail describes a list of issue comments under an issue ticket
type PagureIssueDetail struct {
	Comment      string     `json:"comment"`
	DateCreated  string     `json:"date_created"`
	ID           int64      `json:"id"`
	Notification bool       `json:"notification"`
	User         PagureUser `json:"user"`
}

// PagureCommitInfo describes a commit
type PagureCommitInfo struct {
	Author           string   `json:"author"`
	CommitTime       int64    `json:"commit_time"`
	CommitTimeOffset int      `json:"commit_time_offset"`
	Committer        string   `json:"committer"`
	Hash             string   `json:"hash"`
	Message          string   `json:"message"`
	ParentIDs        []string `json:"parent_ids"`
	TreeID           string   `json:"tree_id"`
}

// PagurePRRresponse describes a list of pull requests under an issue tracker
type PagurePRResponse struct {
	Pagination struct {
		Next *string `json:"next"`
	} `json:"pagination"`
	Requests []struct {
		Branch         string     `json:"branch"`
		BranchFrom     string     `json:"branch_from"`
		CommitStart    string     `json:"commit_start"`
		CommitStop     string     `json:"commit_stop"`
		DateCreated    string     `json:"date_created"`
		FullURL        string     `json:"full_url"`
		ID             int        `json:"id"`
		InitialComment string     `json:"initial_comment"`
		LastUpdated    string     `json:"last_updated"`
		Status         string     `json:"status"`
		Tags           []string   `json:"tags"`
		Title          string     `json:"title"`
		User           PagureUser `json:"user"`
		ClosedAt       string     `json:"closed_at"`
		ClosedBy       PagureUser `json:"closed_by"`
		RepoFrom       struct {
			FullURL string `json:"full_url"`
		} `json:"repo_from"`
	} `json:"requests"`
}

// processDate converts epoch time string to Go formatted time
func processDate(dateStr *string) time.Time {
	date := time.Time{}
	if dateStr == nil || *dateStr == "" {
		return date
	}

	// Try parsing as Unix timestamp first
	unix, err := strconv.Atoi(*dateStr)
	if err == nil {
		date = time.Unix(int64(unix), 0)
		return date
	}

	// Try parsing as YYYY-MM-DD format
	parsedDate, err := time.Parse("2006-01-02", *dateStr)
	if err == nil {
		return parsedDate
	}

	log.Error("Error parsing date '%s': %v", *dateStr, err)
	return date
}

// New returns a downloader related to this factory according MigrateOptions
func (f *PagureDownloaderFactory) New(ctx context.Context, opts base.MigrateOptions) (base.Downloader, error) {
	u, err := url.Parse(opts.CloneAddr)
	if err != nil {
		return nil, err
	}

	var repoName string

	fields := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(fields) == 2 {
		repoName = fields[0] + "/" + strings.TrimSuffix(fields[1], ".git")
	} else if len(fields) == 1 {
		repoName = strings.TrimSuffix(fields[0], ".git")
	} else {
		return nil, fmt.Errorf("invalid path: %s", u.Path)
	}

	u.Path, u.Fragment = "", ""
	log.Info("Create Pagure downloader. BaseURL: %v RepoName: %s", u, repoName)

	return NewPagureDownloader(ctx, u, opts.AuthToken, repoName), nil
}

// GitServiceType returns the type of Git service
func (f *PagureDownloaderFactory) GitServiceType() structs.GitServiceType {
	return structs.PagureService
}

// SetContext sets context
func (d *PagureDownloader) SetContext(ctx context.Context) {
	d.ctx = ctx
}

// NewPagureDownloader creates a new downloader object
func NewPagureDownloader(ctx context.Context, baseURL *url.URL, token, repoName string) *PagureDownloader {
	var privateIssuesOnlyRepo bool
	if token != "" {
		privateIssuesOnlyRepo = true
	}

	downloader := &PagureDownloader{
		ctx:      ctx,
		baseURL:  baseURL,
		repoName: repoName,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy: proxy.Proxy(),
			},
		},
		token:                 token,
		privateIssuesOnlyRepo: privateIssuesOnlyRepo,
		userMap:               make(map[string]*PagureUser),
		milestoneMap:          make(map[int64]string),
		priorities:            make(map[string]string),
	}

	return downloader
}

// String sets the default text for information purposes
func (d *PagureDownloader) String() string {
	return fmt.Sprintf("migration from Pagure server %s [%d]/%s", d.baseURL, d.repoID, d.repoName)
}

// LogString sets the default text for logging purposes
func (d *PagureDownloader) LogString() string {
	if d == nil {
		return "<PagureDownloader nil>"
	}
	return fmt.Sprintf("<PagureDownloader %s [%d]/%s>", d.baseURL, d.repoID, d.repoName)
}

// callAPI handles all the requests made against Pagure
func (d *PagureDownloader) callAPI(endpoint string, parameter map[string]string, result any) error {
	u, err := d.baseURL.Parse(endpoint)
	if err != nil {
		return err
	}

	if parameter != nil {
		query := u.Query()
		for k, v := range parameter {
			query.Set(k, v)
		}
		u.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(d.ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}

	// pagure.io is protected by Anubis and requires proper headers
	req.Header.Add("Accept", "*/*")
	req.Header.Add("User-Agent", "Forgejo/"+setting.AppVer)

	if d.privateIssuesOnlyRepo {
		req.Header.Set("Authorization", "token "+d.token)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	return decoder.Decode(&result)
}

// GetRepoInfo returns repository information from Pagure
func (d *PagureDownloader) GetRepoInfo() (*base.Repository, error) {
	err := d.callAPI("/api/0/"+d.repoName, nil, &d.meta)
	if err != nil {
		return nil, err
	}

	d.repoID, d.priorities = d.meta.ID, d.meta.Priorities

	cloneURL, err := d.baseURL.Parse(d.meta.FullName)
	if err != nil {
		return nil, err
	}
	originalURL, err := d.baseURL.Parse(d.meta.FullName)
	if err != nil {
		return nil, err
	}

	return &base.Repository{
		Name:        d.meta.Name,
		Description: d.meta.Description,
		CloneURL:    cloneURL.String() + ".git",
		OriginalURL: originalURL.String(),
	}, nil
}

// GetMilestones returns milestones information from Pagure
func (d *PagureDownloader) GetMilestones() ([]*base.Milestone, error) {
	milestones := make([]*base.Milestone, 0, len(d.meta.Milestones))
	for name, details := range d.meta.Milestones {
		state := "closed"
		if details.Active {
			state = "open"
		}

		var deadline *time.Time
		if details.Date != nil && *details.Date != "" {
			parsedDeadline := processDate(details.Date)
			deadline = &parsedDeadline
		}
		milestones = append(milestones, &base.Milestone{
			Title:       name,
			Description: "",
			Deadline:    deadline,
			State:       state,
		})
	}

	return milestones, nil
}

// GetLabels returns labels information from Pagure
func (d *PagureDownloader) GetLabels() ([]*base.Label, error) {
	rawLabels := PagureLabelsList{}

	err := d.callAPI("/api/0/"+d.repoName+"/tags/", nil, &rawLabels)
	if err != nil {
		return nil, err
	}

	labels := make([]*base.Label, 0, len(rawLabels.Labels)+len(d.meta.CloseStatuses))

	for _, label := range rawLabels.Labels {
		rawLabel := PagureLabel{}
		err = d.callAPI("/api/0/"+d.repoName+"/tag/"+label, nil, &rawLabel)
		if err != nil {
			return nil, err
		}
		labels = append(labels, &base.Label{
			Name:        label,
			Description: rawLabel.LabelDescription,
			Color:       strings.TrimPrefix(rawLabel.LabelColor, "#"),
		})
	}

	for _, closeStatus := range d.meta.CloseStatuses {
		labels = append(labels, &base.Label{
			Name:        "Closed As/" + closeStatus,
			Description: "Closed with the reason of " + closeStatus,
			Color:       "FF0000",
			Exclusive:   true,
		})
	}

	for _, value := range d.priorities {
		if value != "" {
			labels = append(labels, &base.Label{
				Name:        "Priority/" + value,
				Description: "Priority of " + value,
				Color:       "FF00FF",
				Exclusive:   true,
			})
		}
	}

	return labels, nil
}

// GetIssues returns issue tickets from Pagure
func (d *PagureDownloader) GetIssues(page, perPage int) ([]*base.Issue, bool, error) {
	rawIssues := PagureIssuesResponse{}

	err := d.callAPI(
		"/api/0/"+d.repoName+"/issues",
		map[string]string{
			"page":     strconv.Itoa(page),
			"per_page": strconv.Itoa(perPage),
			"status":   "all",
		},
		&rawIssues,
	)
	if err != nil {
		return nil, false, err
	}

	issues := make([]*base.Issue, 0, len(rawIssues.Issues))
	for _, issue := range rawIssues.Issues {
		log.Debug("Processing issue %d", issue.ID)
		if d.privateIssuesOnlyRepo && !issue.Private {
			log.Info("Skipping issue %d because it is not private and we are only downloading private issues", issue.ID)
			continue
		}
		labels := []*base.Label{}
		for _, tag := range issue.Tags {
			labels = append(labels, &base.Label{Name: tag})
		}

		if issue.CloseStatus != "" {
			labels = append(labels, &base.Label{Name: "Closed As/" + issue.CloseStatus})
		}

		priorityStr := ""
		if issue.Priority != 0 {
			priorityStr = strconv.FormatInt(issue.Priority, 10)
		}

		if priorityStr != "" {
			priorityValue, ok := d.priorities[priorityStr]
			if ok {
				labels = append(labels, &base.Label{Name: "Priority/" + priorityValue})
			}
		}
		log.Trace("Adding issue: %d", issue.ID)

		closedat := processDate(&issue.ClosedAt)

		issues = append(issues, &base.Issue{
			Title:        issue.Title,
			Number:       issue.ID,
			PosterName:   issue.User.Name,
			PosterID:     -1,
			Content:      issue.Content,
			Milestone:    issue.Milestone,
			State:        strings.ToLower(issue.Status),
			Created:      processDate(&issue.DateCreated),
			Updated:      processDate(&issue.LastUpdated),
			Closed:       &closedat,
			Labels:       labels,
			ForeignIndex: issue.ID,
			Context:      PagureIssueContext{IsPullRequest: false},
		})

		if d.maxIssueIndex < issue.ID {
			d.maxIssueIndex = issue.ID
		}
	}
	hasNext := rawIssues.Pagination.Next == nil

	return issues, hasNext, nil
}

// GetComments returns issue comments from Pagure
func (d *PagureDownloader) GetComments(commentable base.Commentable) ([]*base.Comment, bool, error) {
	context, ok := commentable.GetContext().(PagureIssueContext)
	if !ok {
		return nil, false, fmt.Errorf("unexpected context: %+v", commentable.GetContext())
	}

	list := struct {
		Comments []PagureIssueDetail `json:"comments"`
	}{}
	var endpoint string

	if context.IsPullRequest {
		endpoint = fmt.Sprintf("/api/0/%s/pull-request/%d", d.repoName, commentable.GetForeignIndex())
	} else {
		endpoint = fmt.Sprintf("/api/0/%s/issue/%d", d.repoName, commentable.GetForeignIndex())
	}

	err := d.callAPI(endpoint, nil, &list)
	if err != nil {
		log.Error("Error calling API: %v", err)
		return nil, false, err
	}

	comments := make([]*base.Comment, 0, len(list.Comments))
	for _, unit := range list.Comments {
		if len(unit.Comment) == 0 {
			log.Error("Empty comment")
			continue
		}

		log.Trace("Adding comment: %d", unit.ID)
		c := &base.Comment{
			IssueIndex: commentable.GetLocalIndex(),
			Index:      unit.ID,
			PosterName: unit.User.Name,
			PosterID:   -1,
			Content:    unit.Comment,
			Created:    processDate(&unit.DateCreated),
		}
		comments = append(comments, c)
	}

	return comments, true, nil
}

// GetPullRequests returns pull requests from Pagure
func (d *PagureDownloader) GetPullRequests(page, perPage int) ([]*base.PullRequest, bool, error) {
	// Could not figure out how to disable this in opts, so if a private issues only repo,
	// We just return an empty list
	if d.privateIssuesOnlyRepo {
		pullRequests := make([]*base.PullRequest, 0)
		return pullRequests, true, nil
	}

	rawPullRequests := PagurePRResponse{}
	commit := PagureCommitInfo{}

	err := d.callAPI(
		"/api/0/"+d.repoName+"/pull-requests",
		map[string]string{
			"page":     strconv.Itoa(page),
			"per_page": strconv.Itoa(perPage),
			"status":   "all",
		},
		&rawPullRequests,
	)
	if err != nil {
		return nil, false, err
	}

	pullRequests := make([]*base.PullRequest, 0, len(rawPullRequests.Requests))

	for _, pr := range rawPullRequests.Requests {
		var state, baseSHA string
		var merged bool
		labels := []*base.Label{}

		for _, tag := range pr.Tags {
			labels = append(labels, &base.Label{Name: tag})
		}
		mergedtime := processDate(&pr.ClosedAt)

		err = d.callAPI("/api/0/"+d.repoName+"/c/"+pr.CommitStart+"/info", nil, &commit)
		if err != nil {
			return nil, false, err
		}

		if util.ASCIIEqualFold(pr.Status, "merged") {
			state, merged, baseSHA = "closed", true, commit.ParentIDs[0]
		} else if util.ASCIIEqualFold(pr.Status, "open") {
			state, merged, baseSHA = "open", false, commit.ParentIDs[0]
		} else {
			state, merged, baseSHA = "closed", false, commit.ParentIDs[0]
		}

		pullRequests = append(pullRequests, &base.PullRequest{
			Title:      pr.Title,
			Number:     int64(pr.ID),
			PosterName: pr.User.Name,
			PosterID:   -1,
			Content:    pr.InitialComment,
			State:      state,
			Created:    processDate(&pr.DateCreated),
			Updated:    processDate(&pr.LastUpdated),
			MergedTime: &mergedtime,
			Closed:     &mergedtime,
			Merged:     merged,
			Labels:     labels,
			Head: base.PullRequestBranch{
				Ref:      pr.BranchFrom,
				SHA:      pr.CommitStop,
				RepoName: d.repoName,
				CloneURL: pr.RepoFrom.FullURL + ".git",
			},
			Base: base.PullRequestBranch{
				Ref:      pr.Branch,
				SHA:      baseSHA,
				RepoName: d.repoName,
			},
			ForeignIndex: int64(pr.ID),
			PatchURL:     pr.FullURL + ".patch",
			Context:      PagureIssueContext{IsPullRequest: true},
		})

		// SECURITY: Ensure that the PR is safe
		_ = CheckAndEnsureSafePR(pullRequests[len(pullRequests)-1], d.baseURL.String(), d)
	}

	hasNext := rawPullRequests.Pagination.Next == nil

	return pullRequests, hasNext, nil
}

// GetTopics return repository topics from Pagure
func (d *PagureDownloader) GetTopics() ([]string, error) {
	return d.meta.Topics, nil
}
