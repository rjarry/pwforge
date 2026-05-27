// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	gosync "sync"
	"time"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
	"github.com/rjarry/pwforge/sync"
)

type projectState struct {
	linkName  string
	pwProject patchwork.Project
	forge     models.Forge
	mlToForge *sync.MLToForge
	forgeToML *sync.ForgeToML
}

type Server struct {
	conf *config.Config
	pw   *patchwork.Client

	mu          gosync.RWMutex
	projects    map[string]*projectState // by patchwork link_name
	forgeIndex  map[string]*projectState // by "owner/repo"
	lastRefresh time.Time

	mux *http.ServeMux
	http.Server
}

func NewServer(conf *config.Config) *Server {
	pw := patchwork.NewClient(conf.Patchwork.URL, conf.Patchwork.Token)

	s := &Server{
		conf:       conf,
		pw:         pw,
		projects:   make(map[string]*projectState),
		forgeIndex: make(map[string]*projectState),
		mux:        http.NewServeMux(),
	}
	s.mux.HandleFunc("POST /patchwork", s.handlePatchwork)
	s.mux.HandleFunc("POST /forge", s.handleForge)
	s.Handler = s.mux
	s.Addr = conf.Listen

	log.Printf("listening on %s", conf.Listen)

	return s
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
			key := strings.ToLower(owner + "/" + repo)
			forgeIndex[key] = existing
			continue
		}

		p, err := s.initProject(pwp, projConf)
		if err != nil {
			log.Printf("project %q: init failed: %v", pwp.LinkName, err)
			continue
		}

		projects[pwp.LinkName] = p
		key := strings.ToLower(owner + "/" + repo)
		forgeIndex[key] = p
		log.Printf("project %q: %s/%s (list: %s)",
			pwp.LinkName, owner, repo, pwp.ListEmail)
	}

	s.mu.Lock()
	s.projects = projects
	s.forgeIndex = forgeIndex
	s.lastRefresh = time.Now()
	s.mu.Unlock()

	log.Printf("discovered %d project(s)", len(projects))
	return nil
}

func (s *Server) resolveRepo(
	pwp patchwork.Project, projConf *config.ProjectConfig,
) (owner, repo string, ok bool) {
	if projConf != nil && projConf.Owner != "" && projConf.Repo != "" {
		return projConf.Owner, projConf.Repo, true
	}
	owner, repo, ok = parseGitHubRepo(pwp.WebURL)
	if ok {
		return owner, repo, true
	}
	owner, repo, ok = parseGitHubRepo(pwp.ScmURL)
	return owner, repo, ok
}

func parseGitHubRepo(rawURL string) (owner, repo string, ok bool) {
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

	smtpConf := s.conf.SMTP
	if pwp.ListEmail != "" {
		smtpConf.To = pwp.ListEmail
	}

	mlToForge := sync.NewMLToForge(s.pw, forge, &gitConf, &smtpConf, pwp.LinkName)
	forgeToML := sync.NewForgeToML(s.pw, mlToForge.Git(), forge, pwp.LinkName)

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

func (s *Server) findProjectByRepo(owner, repo string) *projectState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := strings.ToLower(owner + "/" + repo)
	return s.forgeIndex[key]
}

func (s *Server) handlePatchwork(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-Patchwork-Signature")
	if !patchwork.VerifySignature(body, sig, s.conf.Patchwork.WebhookSecret) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := patchwork.ParseWebhookEvent(body)
	if err != nil {
		log.Printf("patchwork webhook parse error: %v", err)
		http.Error(w, "parse event", http.StatusBadRequest)
		return
	}

	log.Printf("patchwork event: %s %#v", event.Category, event)
	w.WriteHeader(http.StatusOK)

	go func() {
		if err := s.ensureProjects(); err != nil {
			log.Printf("refresh projects: %v", err)
			return
		}
		p := s.findProject(event.Project.LinkName)
		if p == nil {
			log.Printf("no project found for %q", event.Project.LinkName)
			return
		}
		s.handlePatchworkEvent(event, p)
	}()
}

func (s *Server) handlePatchworkEvent(event *patchwork.Event, p *projectState) {
	switch event.Category {
	case "series-completed":
		seriesID := extractSeriesID(event)
		if seriesID <= 0 {
			break
		}
		if s.linkForgeOriginatedSeries(seriesID, p) {
			break
		}
		if !s.conf.Sync.MLToForge {
			break
		}
		if err := p.mlToForge.HandleSeriesCompleted(seriesID); err != nil {
			log.Printf("series-completed error: %v", err)
		}
	case "patch-comment-created", "cover-comment-created":
		if err := p.mlToForge.HandleCommentCreated(event); err != nil {
			log.Printf("comment-created error: %v", err)
		}
	}
}

func (s *Server) linkForgeOriginatedSeries(seriesID int, p *projectState) bool {
	series, err := s.pw.GetSeries(seriesID)
	if err != nil {
		return false
	}
	if _, ok := series.Metadata[p.forge.MetaKeyPR()].(string); ok {
		return true
	}
	if len(series.Patches) == 0 {
		return false
	}
	patch, err := s.pw.GetPatch(series.Patches[0].ID)
	if err != nil {
		return false
	}
	prRef, ok := patch.Headers[sync.PRHeader].(string)
	if !ok || prRef == "" {
		return false
	}
	log.Printf("series %d originated from forge PR %s, linking", seriesID, prRef)

	prNumber, err := sync.ParsePRNumber(prRef)
	if err != nil {
		log.Printf("invalid PR ref in header: %v", err)
		return false
	}

	branch, _ := patch.Headers[sync.BranchHeader].(string)

	metadata := map[string]interface{}{
		p.forge.MetaKeyPR(): prRef,
	}
	if branch != "" {
		metadata[p.forge.MetaKeyBranch()] = branch
	}
	if err := s.pw.UpdateSeriesMetadata(seriesID, metadata); err != nil {
		log.Printf("failed to link series %d to PR: %v", seriesID, err)
	}

	_ = prNumber
	return true
}

func extractSeriesID(event *patchwork.Event) int {
	if event.Payload.Series != nil {
		return event.Payload.Series.ID
	}
	return 0
}

func (s *Server) handleForge(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if err := s.ensureProjects(); err != nil {
		log.Printf("refresh projects: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	owner, repo := extractRepoFromPayload(body)
	p := s.findProjectByRepo(owner, repo)
	if p == nil {
		log.Printf("forge webhook: no project for %s/%s", owner, repo)
		http.Error(w, "unknown repository", http.StatusNotFound)
		return
	}

	event, err := p.forge.ParseWebhook(body, r.Header)
	if err != nil {
		log.Printf("forge webhook error: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if event == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	log.Printf("forge event: %s %#v", event.Type, event)
	w.WriteHeader(http.StatusOK)

	go s.handleForgeEvent(event, p)
}

func extractRepoFromPayload(body []byte) (string, string) {
	var payload struct {
		Repository struct {
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Repository.Owner.Login, payload.Repository.Name
}

func (s *Server) handleForgeEvent(event *models.ForgeEvent, p *projectState) {
	if event.Type == "pull_request" {
		if !s.conf.Sync.ForgeToML {
			return
		}
		if strings.HasPrefix(event.PRHeadBranch, s.conf.Git.BranchPrefix+"/") {
			log.Printf("ignoring PR #%d from pwforge branch %s",
				event.PRNumber, event.PRHeadBranch)
			return
		}
		if err := p.forgeToML.HandlePullRequest(event); err != nil {
			log.Printf("pull_request error: %v", err)
		}
		return
	}

	series := s.findSeriesByPR(event.PRNumber, p)
	if series == nil {
		log.Printf("no patchwork series found for PR #%d", event.PRNumber)
		return
	}

	switch event.Type {
	case "issue_comment":
		if err := p.forgeToML.HandleIssueComment(event, series); err != nil {
			log.Printf("issue_comment error: %v", err)
		}
	case "review":
		if err := p.forgeToML.HandleReview(event, series); err != nil {
			log.Printf("review error: %v", err)
		}
	case "check_pending":
		if err := p.forgeToML.HandleCheckPending(event, series); err != nil {
			log.Printf("check_pending error: %v", err)
		}
	case "check":
		if err := p.forgeToML.HandleCheckEvent(event, series); err != nil {
			log.Printf("check error: %v", err)
		}
	}
}

func (s *Server) findSeriesByPR(prNumber int, p *projectState) *patchwork.Series {
	prRef := p.forge.PRRef(prNumber)

	matches, err := s.pw.FindSeriesByMetadata(
		p.linkName, p.forge.MetaKeyPR(), prRef)
	if err != nil {
		log.Printf("find series by PR: %v", err)
		return nil
	}
	if len(matches) == 0 {
		return nil
	}
	return &matches[0]
}
