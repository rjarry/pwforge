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
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	gh "github.com/google/go-github/v84/github"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
)

type GitHub struct {
	client        *gh.Client
	owner         string
	repo          string
	forkOwner     string
	forkRepo      string
	webhookSecret string
	ts            *TokenSource
	baseBranch    string
}

func New(conf *config.Config) (models.Forge, error) {
	var ts *TokenSource
	var err error

	if conf.GitHub.AppID != 0 {
		ts, err = NewTokenSourceApp(
			conf.GitHub.AppID,
			conf.GitHub.InstallationID,
			conf.GitHub.PrivateKeyFile,
		)
		if err != nil {
			return nil, err
		}
		log.Printf("using github app authentication (app-id=%d)",
			conf.GitHub.AppID)
	} else {
		ts = NewTokenSourcePAT(conf.GitHub.Token)
		log.Printf("using github personal access token authentication")
	}

	client := gh.NewClient(&http.Client{Transport: ts.Transport()})
	if conf.GitHub.APIURL != "" {
		u, err := url.Parse(conf.GitHub.APIURL)
		if err != nil {
			return nil, fmt.Errorf("parse api-url: %w", err)
		}
		client.BaseURL = u
	}

	forkOwner := conf.GitHub.ForkOwner
	if forkOwner == "" {
		forkOwner = conf.GitHub.Owner
	}
	forkRepo := conf.GitHub.ForkRepo
	if forkRepo == "" {
		forkRepo = conf.GitHub.Repo
	}

	return &GitHub{
		client:        client,
		owner:         conf.GitHub.Owner,
		repo:          conf.GitHub.Repo,
		forkOwner:     forkOwner,
		forkRepo:      forkRepo,
		webhookSecret: conf.GitHub.WebhookSecret,
		ts:            ts,
	}, nil
}

func init() {
	models.RegisterForge("github", New)
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

func (g *GitHub) ParseWebhook(r *http.Request) (*models.ForgeEvent, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if !g.verifySignature(body, sig) {
		return nil, fmt.Errorf("invalid signature")
	}

	eventType := r.Header.Get("X-GitHub-Event")
	log.Printf("github webhook: %s (%d bytes)", eventType, len(body))

	switch eventType {
	case "issue_comment":
		return g.parseIssueComment(body)
	case "pull_request_review_comment":
		return g.parseReviewComment(body)
	case "check_run":
		return g.parseCheckRun(body)
	case "check_suite":
		return g.parseCheckSuite(body)
	case "pull_request":
		return g.parsePullRequest(body)
	default:
		return nil, nil
	}
}

func (g *GitHub) parseIssueComment(body []byte) (*models.ForgeEvent, error) {
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
		PRNumber: payload.GetIssue().GetNumber(),
		Author: models.ForgeUser{
			Login: user.GetLogin(),
			Name:  user.GetName(),
			Email: user.GetEmail(),
		},
		Body: payload.GetComment().GetBody(),
	}, nil
}

func (g *GitHub) parseReviewComment(body []byte) (*models.ForgeEvent, error) {
	var payload gh.PullRequestReviewCommentEvent
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse review_comment: %w", err)
	}
	if payload.GetAction() != "created" {
		return nil, nil
	}
	if strings.Contains(payload.GetComment().GetBody(), models.CommentMarker) {
		return nil, nil
	}
	comment := payload.GetComment()
	user := comment.GetUser()
	return &models.ForgeEvent{
		Type:     "review_comment",
		PRNumber: payload.GetPullRequest().GetNumber(),
		Author: models.ForgeUser{
			Login: user.GetLogin(),
			Name:  user.GetName(),
			Email: user.GetEmail(),
		},
		Body:     comment.GetBody(),
		Path:     comment.GetPath(),
		DiffHunk: comment.GetDiffHunk(),
	}, nil
}

func (g *GitHub) parseCheckRun(body []byte) (*models.ForgeEvent, error) {
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
		PRNumber:    prs[0].GetNumber(),
		CheckName:   run.GetName(),
		CheckStatus: "pending",
		CheckURL:    run.GetHTMLURL(),
	}, nil
}

func (g *GitHub) parseCheckSuite(body []byte) (*models.ForgeEvent, error) {
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

	// fetch individual check runs for this suite
	runs, _, err := g.client.Checks.ListCheckRunsCheckSuite(
		context.Background(), g.owner, g.repo,
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
		PRNumber:    prs[0].GetNumber(),
		CheckName:   suite.GetApp().GetName(),
		CheckStatus: suite.GetConclusion(),
		CheckDesc:   desc.String(),
		CheckRuns:   checkRuns,
	}, nil
}

func (g *GitHub) parsePullRequest(body []byte) (*models.ForgeEvent, error) {
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

func (g *GitHub) verifySignature(body []byte, signature string) bool {
	if g.webhookSecret == "" {
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
	mac := hmac.New(sha256.New, []byte(g.webhookSecret))
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}
