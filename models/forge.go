// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package models

import (
	"fmt"
	"net/http"

	"github.com/rjarry/pwforge/config"
)

const CommentMarker = "<!-- pwforge -->"

type ForgeEvent struct {
	Type           string // "issue_comment", "review_comment", "check", "pull_request"
	PRNumber       int
	Author         ForgeUser
	Body           string
	Path           string
	DiffHunk       string
	ReviewState    string // "approved", "changes_requested", "commented"
	ReviewComments []ReviewComment
	// check event fields
	CheckName   string
	CheckStatus string
	CheckURL    string
	CheckDesc   string
	CheckRuns   []CheckRun
	// pull_request event fields
	PRTitle      string
	PRBody       string
	PRHead       string // refspec: pull/<n>/head
	PRBase       string // refspec: pull/<n>/base
	PRHeadBranch string // branch name (for loop detection)
	PRAction     string
	PRBefore     string // previous head SHA (for synchronize)
}

type ReviewComment struct {
	Path     string
	DiffHunk string
	Body     string
}

type CheckRun struct {
	Name   string
	Status string
	URL    string
	Desc   string
}

type ForgeUser struct {
	Login string
	Name  string
	Email string
}

type Forge interface {
	BaseBranch() (string, error)
	RepoURL() string
	WriteCredentials(path string) error
	MetaKeyPR() string
	MetaKeyBranch() string
	PRRef(prNumber int) string
	PRRefSpec(prNumber int) string
	CreatePR(title, body, head, base string) (int, error)
	PostComment(prNumber int, body string) error
	ParseWebhook(body []byte, headers http.Header) (*ForgeEvent, error)
	VerifyWebhookSignature(body []byte, signature string) bool
	Owner() string
	Repo() string
}

type ForgeConstructor func(conf *config.Config, project *config.ProjectConfig) (Forge, error)

var forges = make(map[string]ForgeConstructor)

func RegisterForge(name string, c ForgeConstructor) {
	forges[name] = c
}

func NewForge(conf *config.Config, project *config.ProjectConfig) (Forge, error) {
	ctor, found := forges[conf.Forge]
	if !found {
		return nil, fmt.Errorf("unknown forge %q", conf.Forge)
	}
	return ctor(conf, project)
}
