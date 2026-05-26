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
	Type     string // "issue_comment", "review_comment", "check"
	PRNumber int
	Author   ForgeUser
	Body     string
	Path     string
	DiffHunk string
	// check event fields
	CheckName   string
	CheckStatus string
	CheckURL    string
	CheckDesc   string
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
	CreatePR(title, body, head, base string) (int, error)
	PostComment(prNumber int, body string) error
	ParseWebhook(r *http.Request) (*ForgeEvent, error)
}

type ForgeConstructor func(*config.Config) (Forge, error)

var forges = make(map[string]ForgeConstructor)

func RegisterForge(name string, c ForgeConstructor) {
	forges[name] = c
}

func NewForge(conf *config.Config) (Forge, error) {
	ctor, found := forges[conf.Forge]
	if !found {
		return nil, fmt.Errorf("unknown forge %q", conf.Forge)
	}
	return ctor(conf)
}
