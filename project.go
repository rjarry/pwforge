// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
)

type projectState struct {
	linkName  string
	pwProject patchwork.Project
	forge     models.Forge
	mlToForge *MLToForge
	forgeToML *ForgeToML
}

func (s *Server) ensureProjects() error {
	s.mu.RLock()
	age := time.Since(s.lastRefresh)
	s.mu.RUnlock()

	ttl := time.Duration(s.conf.Patchwork.CacheTTL) * time.Second
	if age < ttl {
		return nil
	}
	return s.refreshProjects()
}

func (s *Server) refreshProjects() error {
	pwProjects, err := s.pw.ListProjects()
	if err != nil {
		return fmt.Errorf("list patchwork projects: %w", err)
	}

	projects := make(map[string]*projectState)
	forgeIndex := make(map[string]*projectState)

	for _, pwp := range pwProjects {
		if s.conf.Patchwork.Project != "" && pwp.LinkName != s.conf.Patchwork.Project {
			continue
		}

		projConf := s.conf.Projects[pwp.LinkName]

		owner, repo, ok := s.resolveRepo(pwp, projConf)
		if !ok {
			continue
		}

		if projConf == nil {
			projConf = &config.ProjectConfig{}
		}
		if projConf.Owner == "" {
			projConf.Owner = owner
		}
		if projConf.Repo == "" {
			projConf.Repo = repo
		}

		s.mu.RLock()
		existing := s.projects[pwp.LinkName]
		s.mu.RUnlock()

		if existing != nil {
			projects[pwp.LinkName] = existing
			forgeIndex[existing.forge.RepoKey()] = existing
			continue
		}

		p, err := s.initProject(pwp, projConf)
		if err != nil {
			Errorf("project %q: init failed: %v", pwp.LinkName, err)
			continue
		}

		projects[pwp.LinkName] = p
		forgeIndex[p.forge.RepoKey()] = p
		Infof("project %q: %s (list: %s)",
			pwp.LinkName, p.forge.RepoKey(), pwp.ListEmail)
	}

	s.mu.Lock()
	s.projects = projects
	s.forgeIndex = forgeIndex
	s.lastRefresh = time.Now()
	s.mu.Unlock()

	Infof("discovered %d project(s)", len(projects))
	return nil
}

func (s *Server) resolveRepo(
	pwp patchwork.Project, projConf *config.ProjectConfig,
) (owner, repo string, ok bool) {
	if projConf != nil && projConf.Owner != "" && projConf.Repo != "" {
		return projConf.Owner, projConf.Repo, true
	}
	owner, repo, ok = models.ResolveRepo(s.conf.Forge, pwp.WebURL)
	if ok {
		return owner, repo, true
	}
	return models.ResolveRepo(s.conf.Forge, pwp.ScmURL)
}

func (s *Server) initProject(
	pwp patchwork.Project, projConf *config.ProjectConfig,
) (*projectState, error) {
	forge, err := models.NewForge(s.conf, projConf)
	if err != nil {
		return nil, fmt.Errorf("create forge: %w", err)
	}

	gitConf := s.conf.Git
	gitConf.MirrorPath = filepath.Join(s.conf.Git.MirrorPath, pwp.LinkName+".git")
	if projConf.SubjectPrefix != "" {
		gitConf.SubjectPrefix = projConf.SubjectPrefix
	}
	if gitConf.SubjectPrefix == "" {
		gitConf.SubjectPrefix = "PATCH " + pwp.LinkName
	}

	smtpConf := s.conf.SMTP
	if pwp.ListEmail != "" {
		smtpConf.To = pwp.ListEmail
	}

	git := NewGitMirror(&gitConf, &smtpConf, forge)
	mlToForge := NewMLToForge(s.pw, forge, git, pwp.LinkName)
	forgeToML := NewForgeToML(s.pw, forge, git, pwp.LinkName)

	return &projectState{
		linkName:  pwp.LinkName,
		pwProject: pwp,
		forge:     forge,
		mlToForge: mlToForge,
		forgeToML: forgeToML,
	}, nil
}

func (s *Server) findProject(linkName string) *projectState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projects[linkName]
}

func (s *Server) findProjectByKey(key string) *projectState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.forgeIndex[key]
}
