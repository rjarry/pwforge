// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package main

import (
	"io"
	"log"
	"net/http"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
	"github.com/rjarry/pwforge/sync"
)

type Server struct {
	conf      *config.Config
	pw        *patchwork.Client
	forge     models.Forge
	mlToForge *sync.MLToForge
	forgeToML *sync.ForgeToML
	mux       *http.ServeMux
	http.Server
}

func NewServer(conf *config.Config, forge models.Forge) *Server {
	pw := patchwork.NewClient(conf.Patchwork.URL, conf.Patchwork.Token)

	s := &Server{
		conf:      conf,
		pw:        pw,
		forge:     forge,
		mlToForge: sync.NewMLToForge(pw, forge, conf),
		forgeToML: sync.NewForgeToML(pw, &conf.SMTP),
		mux:       http.NewServeMux(),
	}
	s.mux.HandleFunc("POST /patchwork", s.handlePatchwork)
	s.mux.HandleFunc("POST /forge", s.handleForge)
	s.Handler = s.mux
	s.Addr = conf.Listen

	log.Printf("listening on %s", conf.Listen)

	return s
}

func (s *Server) handlePatchwork(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	log.Printf("patchwork webhook: %s", body)

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

	log.Printf("patchwork event: %s (id=%d)", event.Category, event.ID)
	w.WriteHeader(http.StatusOK)

	go s.handlePatchworkEvent(event)
}

func (s *Server) handlePatchworkEvent(event *patchwork.Event) {
	switch event.Category {
	case "series-completed":
		seriesID := extractSeriesID(event)
		if seriesID > 0 {
			if err := s.mlToForge.HandleSeriesCompleted(seriesID); err != nil {
				log.Printf("series-completed error: %v", err)
			}
		}
	case "patch-comment-created", "cover-comment-created":
		if err := s.mlToForge.HandleCommentCreated(event); err != nil {
			log.Printf("comment-created error: %v", err)
		}
	}
}

func extractSeriesID(event *patchwork.Event) int {
	series, ok := event.Payload["series"].(map[string]any)
	if !ok {
		return 0
	}
	id, ok := series["id"].(float64)
	if !ok {
		return 0
	}
	return int(id)
}

func (s *Server) handleForge(w http.ResponseWriter, r *http.Request) {
	event, err := s.forge.ParseWebhook(r)
	if err != nil {
		log.Printf("forge webhook error: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if event == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	log.Printf("forge event: %s (PR #%d by %s)",
		event.Type, event.PRNumber, event.Author.Login)

	series := s.findSeriesByPR(event.PRNumber)
	if series == nil {
		log.Printf("no patchwork series found for PR #%d", event.PRNumber)
		w.WriteHeader(http.StatusOK)
		return
	}

	switch event.Type {
	case "issue_comment":
		if err := s.forgeToML.HandleIssueComment(event, series); err != nil {
			log.Printf("issue_comment error: %v", err)
		}
	case "review_comment":
		if err := s.forgeToML.HandleReviewComment(event, series); err != nil {
			log.Printf("review_comment error: %v", err)
		}
	case "check":
		if err := s.forgeToML.HandleCheckEvent(event, series); err != nil {
			log.Printf("check error: %v", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) findSeriesByPR(prNumber int) *patchwork.Series {
	prRef := s.forge.PRRef(prNumber)

	allSeries, err := s.pw.ListSeries(s.conf.Patchwork.Project)
	if err != nil {
		log.Printf("list series: %v", err)
		return nil
	}
	for i := range allSeries {
		if ref, ok := allSeries[i].Metadata[s.forge.MetaKeyPR()].(string); ok {
			if ref == prRef {
				return &allSeries[i]
			}
		}
	}
	return nil
}
