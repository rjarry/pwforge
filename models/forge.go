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
	RepoKey        string
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
	RepoKey() string
}

type ForgeConstructor func(conf *config.Config, project *config.ProjectConfig) (Forge, error)

type WebhookParser func(body []byte, headers http.Header) (*ForgeEvent, error)

type WebhookParserFactory func(conf *config.Config) (WebhookParser, error)

type RepoResolver func(rawURL string) (owner, repo string, ok bool)

type SetupHandlerFactory func(conf *config.Config) http.Handler

type forgeType struct {
	newForge     ForgeConstructor
	newParser    WebhookParserFactory
	resolveRepo  RepoResolver
	setupHandler SetupHandlerFactory
}

var forges = make(map[string]*forgeType)

type ForgeOption func(*forgeType)

func WithSetupHandler(f SetupHandlerFactory) ForgeOption {
	return func(ft *forgeType) { ft.setupHandler = f }
}

func RegisterForge(
	name string,
	c ForgeConstructor,
	p WebhookParserFactory,
	r RepoResolver,
	opts ...ForgeOption,
) {
	ft := &forgeType{
		newForge:    c,
		newParser:   p,
		resolveRepo: r,
	}
	for _, opt := range opts {
		opt(ft)
	}
	forges[name] = ft
}

func NewForge(conf *config.Config, project *config.ProjectConfig) (Forge, error) {
	ft, found := forges[conf.Forge]
	if !found {
		return nil, fmt.Errorf("unknown forge %q", conf.Forge)
	}
	return ft.newForge(conf, project)
}

func NewWebhookParser(conf *config.Config) (WebhookParser, error) {
	ft, found := forges[conf.Forge]
	if !found {
		return nil, fmt.Errorf("unknown forge %q", conf.Forge)
	}
	return ft.newParser(conf)
}

func ResolveRepo(forge, rawURL string) (owner, repo string, ok bool) {
	ft, found := forges[forge]
	if !found {
		return "", "", false
	}
	return ft.resolveRepo(rawURL)
}

func NewSetupHandler(conf *config.Config) http.Handler {
	ft, found := forges[conf.Forge]
	if !found || ft.setupHandler == nil {
		return nil
	}
	return ft.setupHandler(conf)
}
