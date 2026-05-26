// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package sync

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
)

type MLToForge struct {
	pw           *patchwork.Client
	forge        models.Forge
	git          *GitMirror
	branchPrefix string
}

func NewMLToForge(
	pw *patchwork.Client, forge models.Forge, conf *config.Config,
) *MLToForge {
	return &MLToForge{
		pw:           pw,
		forge:        forge,
		git:          NewGitMirror(&conf.Git, forge),
		branchPrefix: conf.Git.BranchPrefix,
	}
}

func (m *MLToForge) Git() *GitMirror { return m.git }

func (m *MLToForge) HandleSeriesCompleted(seriesID int) error {
	series, err := m.pw.GetSeries(seriesID)
	if err != nil {
		return fmt.Errorf("get series %d: %w", seriesID, err)
	}

	if prRef, ok := series.Metadata[m.forge.MetaKeyPR()].(string); ok && prRef != "" {
		log.Printf("series %d already has PR %s, skipping", seriesID, prRef)
		return nil
	}

	// check if this is a respin of a previous series that has a PR
	prev := m.findPreviousPR(series)
	if prev != nil {
		return m.updateExistingPR(series, prev)
	}

	return m.createNewPR(series)
}

func (m *MLToForge) findPreviousPR(series *patchwork.Series) *patchwork.Series {
	if series.PreviousSeries == nil {
		return nil
	}
	// walk back through previous versions to find one with a PR
	prevURL := *series.PreviousSeries
	for prevURL != "" {
		prevID, err := parseIDFromURL(prevURL)
		if err != nil {
			log.Printf("invalid previous_series URL %q: %v", prevURL, err)
			return nil
		}
		prev, err := m.pw.GetSeries(prevID)
		if err != nil {
			log.Printf("get previous series %d: %v", prevID, err)
			return nil
		}
		if _, ok := prev.Metadata[m.forge.MetaKeyPR()].(string); ok {
			return prev
		}
		if prev.PreviousSeries == nil {
			return nil
		}
		prevURL = *prev.PreviousSeries
	}
	return nil
}

func (m *MLToForge) updateExistingPR(series, prev *patchwork.Series) error {
	prRef := prev.Metadata[m.forge.MetaKeyPR()].(string)
	branch := prev.Metadata[m.forge.MetaKeyBranch()].(string)

	prNumber, err := ParsePRNumber(prRef)
	if err != nil {
		return err
	}

	baseBranch, err := m.forge.BaseBranch()
	if err != nil {
		return fmt.Errorf("base branch: %w", err)
	}

	// force-push the new version to the same branch
	if err := m.applyAndPush(series, branch, baseBranch); err != nil {
		return fmt.Errorf("apply and push series %d: %w", series.ID, err)
	}

	// post a comment about the update
	comment := m.buildUpdateComment(series)
	if err := m.forge.PostComment(prNumber, comment); err != nil {
		log.Printf("failed to post update comment on PR #%d: %v", prNumber, err)
	}

	// copy the PR metadata to the new series
	metadata := series.Metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadata[m.forge.MetaKeyPR()] = prRef
	metadata[m.forge.MetaKeyBranch()] = branch

	if err := m.pw.UpdateSeriesMetadata(series.ID, metadata); err != nil {
		log.Printf("failed to update series %d metadata: %v", series.ID, err)
	}

	log.Printf("updated PR #%d with series %d (v%d)", prNumber, series.ID, series.Version)
	return nil
}

func (m *MLToForge) createNewPR(series *patchwork.Series) error {
	baseBranch, err := m.forge.BaseBranch()
	if err != nil {
		return fmt.Errorf("base branch: %w", err)
	}

	branch := m.branchName(series)

	if err := m.applyAndPush(series, branch, baseBranch); err != nil {
		return fmt.Errorf("apply and push series %d: %w", series.ID, err)
	}

	prNumber, err := m.forge.CreatePR(
		series.Name, m.buildPRBody(series), branch, baseBranch,
	)
	if err != nil {
		return fmt.Errorf("create PR for series %d: %w", series.ID, err)
	}

	metadata := series.Metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}
	metadata[m.forge.MetaKeyPR()] = m.forge.PRRef(prNumber)
	metadata[m.forge.MetaKeyBranch()] = branch

	if err := m.pw.UpdateSeriesMetadata(series.ID, metadata); err != nil {
		log.Printf("failed to update series %d metadata: %v", series.ID, err)
	}

	log.Printf("created PR #%d for series %d", prNumber, series.ID)
	return nil
}

func (m *MLToForge) buildUpdateComment(series *patchwork.Series) string {
	var b strings.Builder

	fmt.Fprintf(&b, "> Series updated to v%d by %s <%s>.\n>\n",
		series.Version, series.Submitter.Name, series.Submitter.Email)

	fmt.Fprintf(&b, "> [View on Patchwork](%s)\n\n", series.WebURL)

	_, freeform := m.seriesContent(series, series.Submitter.Email)
	if freeform != "" {
		fmt.Fprintf(&b, "---\n\n%s\n\n", freeform)
	}

	b.WriteString(models.CommentMarker)

	return b.String()
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func (m *MLToForge) branchName(series *patchwork.Series) string {
	name := series.Name
	if name == "" && len(series.Patches) > 0 {
		name = series.Patches[0].Name
	}
	slug := strings.ToLower(name)
	slug = nonAlnum.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 50 {
		slug = slug[:50]
		slug = strings.TrimRight(slug, "-")
	}
	return fmt.Sprintf("%s/%x/%s", m.branchPrefix, series.ID, slug)
}

func parseIDFromURL(url string) (int, error) {
	// URL format: http://host/api/1.5/series/42/
	url = strings.TrimRight(url, "/")
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return 0, fmt.Errorf("invalid URL: %s", url)
	}
	return strconv.Atoi(url[i+1:])
}

func (m *MLToForge) applyAndPush(series *patchwork.Series, branch, baseBranch string) error {
	if err := m.git.EnsureMirror(); err != nil {
		return err
	}
	if err := m.git.Fetch(); err != nil {
		return err
	}

	workdir, err := os.MkdirTemp("", "pwforge-*")
	if err != nil {
		return err
	}

	if err := m.git.AddWorktree(baseBranch, workdir); err != nil {
		os.RemoveAll(workdir)
		return err
	}
	defer func() { _ = m.git.DelWorktree(workdir) }()

	mbox, err := m.pw.GetSeriesMbox(series.ID)
	if err != nil {
		return fmt.Errorf("get mbox: %w", err)
	}

	if err := m.git.ApplyMbox(workdir, mbox); err != nil {
		return fmt.Errorf("git am: %w", err)
	}

	return m.git.Push(workdir, branch)
}

func (m *MLToForge) buildPRBody(series *patchwork.Series) string {
	var b strings.Builder

	body, freeform := m.seriesContent(series, series.Submitter.Email)
	if body != "" {
		b.WriteString(body)
		b.WriteString("\n\n")
	}
	if freeform != "" {
		b.WriteString(freeform)
		b.WriteString("\n\n")
	}

	fmt.Fprintf(&b, "> Submitted by %s <%s> on the mailing list.\n",
		series.Submitter.Name, series.Submitter.Email)
	fmt.Fprintf(&b, ">\n> [View on Patchwork](%s)\n\n", series.WebURL)
	b.WriteString(models.CommentMarker)

	return b.String()
}

func (m *MLToForge) seriesContent(
	series *patchwork.Series, authorEmail string,
) (body, freeform string) {
	var content string
	if series.CoverLetter != nil {
		cover, err := m.pw.GetCover(series.CoverLetter.ID)
		if err == nil {
			content = cover.Content
		}
	} else if len(series.Patches) == 1 {
		patch, err := m.pw.GetPatch(series.Patches[0].ID)
		if err == nil {
			content = patch.Content
		}
	}
	if content == "" {
		return "", ""
	}
	return parseContent(content, authorEmail)
}

func parseContent(content, authorEmail string) (body, freeform string) {
	// split on the --- separator
	parts := strings.SplitN(content, "\n---\n", 2)

	// first part: commit message body, strip trailers from the author
	body = stripTrailers(parts[0], authorEmail)

	// second part (if any): free-form text, strip diffstat
	if len(parts) > 1 {
		freeform = stripDiffstat(parts[1])
	}

	return strings.TrimSpace(body), strings.TrimSpace(freeform)
}

func stripTrailers(text, authorEmail string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	// remove trailing lines that look like trailers from the author
	for len(lines) > 0 {
		line := lines[len(lines)-1]
		if !isTrailer(line, authorEmail) {
			break
		}
		lines = lines[:len(lines)-1]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isTrailer(line, authorEmail string) bool {
	// standard git trailers: "Key: Value"
	if idx := strings.Index(line, ": "); idx > 0 {
		// only strip trailers that contain the author's email
		if strings.Contains(line, authorEmail) {
			return true
		}
	}
	return false
}

func stripDiffstat(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	// remove trailing diffstat lines (e.g. " file | 3 ++-")
	// and summary line (e.g. " 1 file changed, 2 insertions(+)")
	for len(lines) > 0 {
		line := lines[len(lines)-1]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isDiffstatLine(trimmed) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isDiffstatLine(line string) bool {
	// " path/to/file | 3 ++-" or " 1 file changed, 2 insertions(+)"
	if strings.Contains(line, " | ") {
		return true
	}
	if strings.Contains(line, " changed") &&
		(strings.Contains(line, "insertion") ||
			strings.Contains(line, "deletion")) {
		return true
	}
	return false
}

func (m *MLToForge) HandleCommentCreated(event *patchwork.Event) error {
	var seriesID int
	var commentBody string
	var commentAuthor string

	if patch, ok := event.Payload["patch"].(map[string]interface{}); ok {
		patchID := int(patch["id"].(float64))
		fullPatch, err := m.pw.GetPatch(patchID)
		if err != nil {
			return fmt.Errorf("get patch %d: %w", patchID, err)
		}
		if fullPatch.Metadata == nil {
			return fmt.Errorf("patch %d has no series", patchID)
		}
		comments, err := m.pw.GetPatchComments(patchID)
		if err != nil || len(comments) == 0 {
			return fmt.Errorf("get comments for patch %d: %w", patchID, err)
		}
		last := comments[len(comments)-1]
		commentBody = last.Content
		commentAuthor = fmt.Sprintf("%s <%s>",
			last.Submitter.Name, last.Submitter.Email)

		if s, ok := fullPatch.Metadata["series"].([]interface{}); ok && len(s) > 0 {
			if sid, ok := s[0].(map[string]interface{})["id"].(float64); ok {
				seriesID = int(sid)
			}
		}
	} else if cover, ok := event.Payload["cover"].(map[string]interface{}); ok {
		coverID := int(cover["id"].(float64))
		comments, err := m.pw.GetCoverComments(coverID)
		if err != nil || len(comments) == 0 {
			return fmt.Errorf("get comments for cover %d: %w", coverID, err)
		}
		last := comments[len(comments)-1]
		commentBody = last.Content
		commentAuthor = fmt.Sprintf("%s <%s>",
			last.Submitter.Name, last.Submitter.Email)
	}

	if seriesID == 0 {
		log.Printf("could not determine series for comment event %d", event.ID)
		return nil
	}

	series, err := m.pw.GetSeries(seriesID)
	if err != nil {
		return fmt.Errorf("get series %d: %w", seriesID, err)
	}

	prRef, ok := series.Metadata[m.forge.MetaKeyPR()].(string)
	if !ok || prRef == "" {
		log.Printf("series %d has no sync PR, skipping comment", seriesID)
		return nil
	}

	prNumber, err := ParsePRNumber(prRef)
	if err != nil {
		return err
	}

	body := fmt.Sprintf("**%s** wrote on the mailing list:\n\n> %s\n\n%s",
		commentAuthor,
		strings.ReplaceAll(commentBody, "\n", "\n> "),
		models.CommentMarker,
	)

	return m.forge.PostComment(prNumber, body)
}

func ParsePRNumber(ref string) (int, error) {
	i := strings.LastIndex(ref, "/")
	if i < 0 {
		return 0, fmt.Errorf("invalid PR ref: %s", ref)
	}
	return strconv.Atoi(ref[i+1:])
}
