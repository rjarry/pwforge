// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	gh "github.com/google/go-github/v84/github"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
)

type GitHub struct {
	client     *gh.Client
	owner      string
	repo       string
	forkOwner  string
	forkRepo   string
	ts         *TokenSource
	baseBranch string
}

func newClient(conf *config.Config) (*gh.Client, *TokenSource, error) {
	var ts *TokenSource
	var err error

	if conf.GitHub.AppID != 0 {
		ts, err = NewTokenSourceApp(
			conf.GitHub.AppID,
			conf.GitHub.InstallationID,
			conf.GitHub.PrivateKeyFile,
		)
		if err != nil {
			return nil, nil, err
		}
	} else {
		ts = NewTokenSourcePAT(conf.GitHub.Token)
	}

	client := gh.NewClient(&http.Client{Transport: ts.Transport()})
	if conf.GitHub.APIURL != "" {
		u, err := url.Parse(conf.GitHub.APIURL)
		if err != nil {
			return nil, nil, fmt.Errorf("parse api-url: %w", err)
		}
		client.BaseURL = u
	}

	return client, ts, nil
}

func New(conf *config.Config, project *config.ProjectConfig) (models.Forge, error) {
	client, ts, err := newClient(conf)
	if err != nil {
		return nil, err
	}

	forkOwner := project.ForkOwner
	if forkOwner == "" {
		forkOwner = project.Owner
	}
	forkRepo := project.ForkRepo
	if forkRepo == "" {
		forkRepo = project.Repo
	}

	return &GitHub{
		client:    client,
		owner:     project.Owner,
		repo:      project.Repo,
		forkOwner: forkOwner,
		forkRepo:  forkRepo,
		ts:        ts,
	}, nil
}

func init() {
	models.RegisterForge("github", New, newWebhookParser, resolveRepo,
		models.WithSetupHandler(SetupHandler))
}

func (g *GitHub) BaseBranch() (string, error) {
	if g.baseBranch != "" {
		return g.baseBranch, nil
	}
	repo, _, err := g.client.Repositories.Get(
		context.Background(), g.owner, g.repo,
	)
	if err != nil {
		return "", fmt.Errorf("get repo: %w", err)
	}
	g.baseBranch = repo.GetDefaultBranch()
	return g.baseBranch, nil
}

func (g *GitHub) RepoURL() string {
	return fmt.Sprintf("https://github.com/%s/%s.git", g.forkOwner, g.forkRepo)
}

func (g *GitHub) WriteCredentials(path string) error {
	token, err := g.ts.Token()
	if err != nil {
		return err
	}
	line := fmt.Sprintf("https://x-access-token:%s@github.com\n", token)
	return os.WriteFile(path, []byte(line), 0o600)
}

func (g *GitHub) RepoKey() string       { return strings.ToLower(g.owner + "/" + g.repo) }
func (g *GitHub) MetaKeyPR() string     { return "github_pr" }
func (g *GitHub) MetaKeyBranch() string { return "github_branch" }

func (g *GitHub) PRRef(prNumber int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d",
		g.owner, g.repo, prNumber)
}

func (g *GitHub) PRRefSpec(prNumber int) string {
	return fmt.Sprintf("pull/%d/head", prNumber)
}

func (g *GitHub) CreatePR(title, body, head, base string) (int, error) {
	if g.forkOwner != g.owner {
		head = g.forkOwner + ":" + head
	}
	pr, _, err := g.client.PullRequests.Create(
		context.Background(),
		g.owner, g.repo,
		&gh.NewPullRequest{
			Title: gh.Ptr(title),
			Head:  gh.Ptr(head),
			Base:  gh.Ptr(base),
			Body:  gh.Ptr(body),
		},
	)
	if err != nil {
		return 0, err
	}
	return pr.GetNumber(), nil
}

func (g *GitHub) PostComment(prNumber int, body string) error {
	_, _, err := g.client.Issues.CreateComment(
		context.Background(),
		g.owner, g.repo, prNumber,
		&gh.IssueComment{Body: gh.Ptr(body)},
	)
	return err
}

// webhookParser handles forge webhook parsing independently of any
// specific project. It uses the shared API client and webhook secret.
type webhookParser struct {
	client *gh.Client
	secret string
}

func newWebhookParser(conf *config.Config) (models.WebhookParser, error) {
	client, _, err := newClient(conf)
	if err != nil {
		return nil, err
	}
	p := &webhookParser{
		client: client,
		secret: conf.GitHub.WebhookSecret,
	}
	return p.parse, nil
}

func repoKey(payload interface{ GetRepo() *gh.Repository }) string {
	repo := payload.GetRepo()
	if repo == nil {
		return ""
	}
	return strings.ToLower(repo.GetFullName())
}

func (p *webhookParser) parse(body []byte, headers http.Header) (*models.ForgeEvent, error) {
	sig := headers.Get("X-Hub-Signature-256")
	if !verifySignature(body, sig, p.secret) {
		return nil, fmt.Errorf("invalid signature")
	}

	eventType := headers.Get("X-GitHub-Event")

	switch eventType {
	case "issue_comment":
		return p.parseIssueComment(body)
	case "pull_request_review":
		return p.parseReview(body)
	case "check_run":
		return p.parseCheckRun(body)
	case "check_suite":
		return p.parseCheckSuite(body)
	case "pull_request":
		return p.parsePullRequest(body)
	default:
		return nil, nil
	}
}

func (p *webhookParser) parseIssueComment(body []byte) (*models.ForgeEvent, error) {
	var payload gh.IssueCommentEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse issue_comment: %w", err)
	}
	if payload.GetAction() != "created" {
		return nil, nil
	}
	if !payload.GetIssue().IsPullRequest() {
		return nil, nil
	}
	if strings.Contains(payload.GetComment().GetBody(), models.CommentMarker) {
		return nil, nil
	}
	user := payload.GetComment().GetUser()
	return &models.ForgeEvent{
		Type:     "issue_comment",
		RepoKey:  repoKey(&payload),
		PRNumber: payload.GetIssue().GetNumber(),
		Author: models.ForgeUser{
			Login: user.GetLogin(),
			Name:  user.GetName(),
			Email: user.GetEmail(),
		},
		Body: payload.GetComment().GetBody(),
	}, nil
}

func (p *webhookParser) parseReview(body []byte) (*models.ForgeEvent, error) {
	var payload gh.PullRequestReviewEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse pull_request_review: %w", err)
	}
	if payload.GetAction() != "submitted" {
		return nil, nil
	}
	review := payload.GetReview()
	if strings.Contains(review.GetBody(), models.CommentMarker) {
		return nil, nil
	}
	user := review.GetUser()
	pr := payload.GetPullRequest()
	repo := payload.GetRepo()

	comments, _, err := p.client.PullRequests.ListReviewComments(
		context.Background(),
		repo.GetOwner().GetLogin(), repo.GetName(),
		pr.GetNumber(), review.GetID(), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("list review comments: %w", err)
	}

	var reviewComments []models.ReviewComment
	for _, c := range comments {
		reviewComments = append(reviewComments, models.ReviewComment{
			Path:     c.GetPath(),
			DiffHunk: c.GetDiffHunk(),
			Body:     c.GetBody(),
		})
	}

	return &models.ForgeEvent{
		Type:     "review",
		RepoKey:  repoKey(&payload),
		PRNumber: pr.GetNumber(),
		Author: models.ForgeUser{
			Login: user.GetLogin(),
			Name:  user.GetName(),
			Email: user.GetEmail(),
		},
		Body:           review.GetBody(),
		ReviewState:    review.GetState(),
		ReviewComments: reviewComments,
	}, nil
}

func (p *webhookParser) parseCheckRun(body []byte) (*models.ForgeEvent, error) {
	var payload gh.CheckRunEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse check_run: %w", err)
	}
	if payload.GetAction() != "created" {
		return nil, nil
	}
	run := payload.GetCheckRun()
	prs := run.PullRequests
	if len(prs) == 0 {
		return nil, nil
	}
	return &models.ForgeEvent{
		Type:        "check_pending",
		RepoKey:     repoKey(&payload),
		PRNumber:    prs[0].GetNumber(),
		CheckName:   run.GetName(),
		CheckStatus: "pending",
		CheckURL:    run.GetHTMLURL(),
	}, nil
}

func (p *webhookParser) parseCheckSuite(body []byte) (*models.ForgeEvent, error) {
	var payload gh.CheckSuiteEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse check_suite: %w", err)
	}
	if payload.GetAction() != "completed" {
		return nil, nil
	}
	suite := payload.GetCheckSuite()
	prs := suite.PullRequests
	if len(prs) == 0 {
		return nil, nil
	}

	repo := payload.GetRepo()
	runs, _, err := p.client.Checks.ListCheckRunsCheckSuite(
		context.Background(),
		repo.GetOwner().GetLogin(), repo.GetName(),
		suite.GetID(), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("list check runs: %w", err)
	}

	var checkRuns []models.CheckRun
	var desc strings.Builder
	for _, run := range runs.CheckRuns {
		status := run.GetConclusion()
		if status == "" {
			status = run.GetStatus()
		}
		var runDesc string
		if output := run.GetOutput(); output != nil {
			runDesc = output.GetSummary()
		}
		checkRuns = append(checkRuns, models.CheckRun{
			Name:   run.GetName(),
			Status: status,
			URL:    run.GetHTMLURL(),
			Desc:   runDesc,
		})
		fmt.Fprintf(&desc, "%s %s", run.GetName(), status)
		if url := run.GetHTMLURL(); url != "" {
			fmt.Fprintf(&desc, ": %s", url)
		}
		desc.WriteString("\n")
	}

	return &models.ForgeEvent{
		Type:        "check",
		RepoKey:     repoKey(&payload),
		PRNumber:    prs[0].GetNumber(),
		CheckName:   suite.GetApp().GetName(),
		CheckStatus: suite.GetConclusion(),
		CheckDesc:   desc.String(),
		CheckRuns:   checkRuns,
	}, nil
}

func (p *webhookParser) parsePullRequest(body []byte) (*models.ForgeEvent, error) {
	var payload gh.PullRequestEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse pull_request: %w", err)
	}
	action := payload.GetAction()
	if action != "opened" && action != "synchronize" {
		return nil, nil
	}
	pr := payload.GetPullRequest()
	user := pr.GetUser()
	return &models.ForgeEvent{
		Type:     "pull_request",
		RepoKey:  repoKey(&payload),
		PRNumber: pr.GetNumber(),
		Author: models.ForgeUser{
			Login: user.GetLogin(),
			Name:  user.GetName(),
			Email: user.GetEmail(),
		},
		PRTitle:      pr.GetTitle(),
		PRBody:       pr.GetBody(),
		PRHead:       fmt.Sprintf("pull/%d/head", pr.GetNumber()),
		PRBase:       pr.GetBase().GetSHA(),
		PRHeadBranch: pr.GetHead().GetRef(),
		PRAction:     action,
		PRBefore:     payload.GetBefore(),
	}, nil
}

func verifySignature(body []byte, signature, secret string) bool {
	if secret == "" {
		return true
	}
	prefix := "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	sig, err := hex.DecodeString(signature[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

func resolveRepo(rawURL string) (owner, repo string, ok bool) {
	if rawURL == "" {
		return "", "", false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", false
	}
	if u.Host != "github.com" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	repo = strings.TrimSuffix(parts[1], ".git")
	return parts[0], repo, true
}
