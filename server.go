// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
)

type Server struct {
	conf   *config.Config
	pw     *patchwork.Client
	parser models.WebhookParser

	mu          sync.RWMutex
	projects    map[string]*projectState // by patchwork link_name
	forgeIndex  map[string]*projectState // by forge repo key
	lastRefresh time.Time

	pwEvents    chan *patchwork.Event
	forgeEvents chan *models.ForgeEvent
	wg          sync.WaitGroup

	mux *http.ServeMux
	http.Server
}

func NewServer(conf *config.Config) (net.Listener, *Server, error) {
	pw := patchwork.NewClient(conf.Patchwork.URL, conf.Patchwork.Token)

	parser, err := models.NewWebhookParser(conf)
	if err != nil {
		return nil, nil, fmt.Errorf("webhook parser: %w", err)
	}

	s := &Server{
		conf:        conf,
		pw:          pw,
		parser:      parser,
		projects:    make(map[string]*projectState),
		forgeIndex:  make(map[string]*projectState),
		mux:         http.NewServeMux(),
		pwEvents:    make(chan *patchwork.Event, conf.QueueSize),
		forgeEvents: make(chan *models.ForgeEvent, conf.QueueSize),
	}
	s.mux.HandleFunc("POST /patchwork", s.handlePatchwork)
	s.mux.HandleFunc("POST /forge", s.handleForge)
	if h := models.NewSetupHandler(conf); h != nil {
		s.mux.Handle("/", h)
	}
	s.Handler = s.mux
	s.Addr = conf.Listen

	s.wg.Add(2)
	go s.handlePatchworkEvents()
	go s.handleForgeEvents()

	sock, err := net.Listen("tcp", conf.Listen)
	if err != nil {
		return nil, nil, err
	}

	return sock, s, nil
}

func (s *Server) StopQueues() {
	close(s.pwEvents)
	close(s.forgeEvents)
	s.wg.Wait()
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
		Errorf("patchwork webhook: %v", err)
		http.Error(w, "parse event", http.StatusBadRequest)
		return
	}

	s.pwEvents <- event

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePatchworkEvents() {
	defer s.wg.Done()
	for event := range s.pwEvents {
		Infof("patchwork event: %s #%d", event.Category, event.ID)
		if err := s.handlePatchworkEvent(event); err != nil {
			Errorf("patchwork event: %s #%d: %v", event.Category, event.ID, err)
		}
	}
}

func (s *Server) handlePatchworkEvent(event *patchwork.Event) error {
	if err := s.ensureProjects(); err != nil {
		return fmt.Errorf("refresh projects: %w", err)
	}
	p := s.findProject(event.Project.LinkName)
	if p == nil {
		return fmt.Errorf("no project found for %q", event.Project.LinkName)
	}

	switch event.Category {
	case "series-completed":
		if event.Payload.Series == nil {
			break
		}
		if s.linkForgeOriginatedSeries(event.Payload.Series.ID, p) {
			break // link successful
		}
		if !s.conf.Sync.MLToForge {
			break
		}
		return p.mlToForge.HandleSeriesCompleted(event.Payload.Series.ID)
	case "patch-comment-created", "cover-comment-created":
		return p.mlToForge.HandleCommentCreated(event)
	}

	return nil
}

func (s *Server) linkForgeOriginatedSeries(seriesID int, p *projectState) bool {
	series, err := s.pw.GetSeries(seriesID)
	if err != nil {
		return false
	}
	if _, ok := series.Metadata[p.forge.MetaKeyPR()].(string); ok {
		return true // already linked
	}
	if len(series.Patches) == 0 {
		return false
	}
	patch, err := s.pw.GetPatch(series.Patches[0].ID)
	if err != nil {
		return false
	}
	prRef, ok := patch.Headers[PRHeader].(string)
	if !ok || prRef == "" {
		return false
	}

	Infof("series %d originated from forge PR %s, linking", seriesID, prRef)

	prNumber, err := ParsePRNumber(prRef)
	if err != nil {
		Errorf("invalid PR ref in header: %v", err)
		return false
	}

	branch, _ := patch.Headers[BranchHeader].(string)

	metadata := map[string]any{p.forge.MetaKeyPR(): prRef}
	if branch != "" {
		metadata[p.forge.MetaKeyBranch()] = branch
	}
	if err := s.pw.UpdateSeriesMetadata(seriesID, metadata); err != nil {
		Errorf("failed to link series %d to PR: %v", seriesID, err)
	}

	_ = prNumber
	return true // successfully linked
}

func (s *Server) handleForge(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	event, err := s.parser(body, r.Header)
	if err != nil {
		Errorf("forge webhook: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if event != nil {
		s.forgeEvents <- event
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleForgeEvents() {
	defer s.wg.Done()
	for event := range s.forgeEvents {
		msg := fmt.Sprintf("%s %s#%d", event.Type,
			event.RepoKey, event.PRNumber)
		Infof("forge event: %s", msg)
		if err := s.handleForgeEvent(event); err != nil {
			Errorf("forge event: %s: %s", msg, err)
		}
	}
}

func (s *Server) handleForgeEvent(event *models.ForgeEvent) error {
	if err := s.ensureProjects(); err != nil {
		return fmt.Errorf("refresh projects: %w", err)
	}
	p := s.findProjectByKey(event.RepoKey)
	if p == nil {
		return fmt.Errorf("forge event: no project for %q", event.RepoKey)
	}
	if event.Type == "pull_request" {
		if !s.conf.Sync.ForgeToML {
			return nil
		}
		if strings.HasPrefix(event.PRHeadBranch, s.conf.Git.BranchPrefix+"/") {
			Infof("ignoring PR #%d from pwforge branch %s",
				event.PRNumber, event.PRHeadBranch)
			return nil
		}
		return p.forgeToML.HandlePullRequest(event)
	}

	series := s.findSeriesByPR(event.PRNumber, p)
	if series == nil {
		Warnf("no patchwork series found for PR #%d", event.PRNumber)
		return nil
	}

	switch event.Type {
	case "issue_comment":
		return p.forgeToML.HandleIssueComment(event, series)
	case "review":
		return p.forgeToML.HandleReview(event, series)
	case "check_pending":
		return p.forgeToML.HandleCheckPending(event, series)
	case "check":
		return p.forgeToML.HandleCheckEvent(event, series)
	}

	return nil
}

func (s *Server) findSeriesByPR(prNumber int, p *projectState) *patchwork.Series {
	prRef := p.forge.PRRef(prNumber)

	matches, err := s.pw.FindSeriesByMetadata(
		p.linkName, p.forge.MetaKeyPR(), prRef)
	if err != nil {
		return nil
	}
	if len(matches) == 0 {
		return nil
	}
	return &matches[0]
}
