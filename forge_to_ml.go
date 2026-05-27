// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
)

type ForgeToML struct {
	pw      *patchwork.Client
	forge   models.Forge
	git     *GitMirror
	project string
}

func NewForgeToML(
	pw *patchwork.Client, forge models.Forge, git *GitMirror, project string,
) *ForgeToML {
	return &ForgeToML{pw: pw, git: git, forge: forge, project: project}
}

const EventHeader = "X-PWForge-Event"

func (g *ForgeToML) HandleIssueComment(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	if series.CoverLetter == nil {
		Infof("series %d has no cover letter, using first patch", series.ID)
	}

	replyTo := g.replyToMsgID(series, "")
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d", series.ID)
	}

	from := g.senderAddress(event.Author)
	subject := fmt.Sprintf("Re: %s (comment)", series.Name)

	return g.git.SendEmail(from, subject, event.Body, replyTo,
		EventHeader+": comment")
}

func (g *ForgeToML) HandleReview(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	replyTo := g.replyToMsgID(series, "")
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d", series.ID)
	}

	from := g.senderAddress(event.Author)
	subject := fmt.Sprintf("Re: %s (review: %s)", series.Name, event.ReviewState)

	var msgBody strings.Builder
	if event.ReviewState != "" {
		fmt.Fprintf(&msgBody, "Review: %s\n\n", event.ReviewState)
	}
	if event.Body != "" {
		msgBody.WriteString(event.Body)
		msgBody.WriteString("\n\n")
	}
	for _, c := range event.ReviewComments {
		fmt.Fprintf(&msgBody, "--- %s\n", c.Path)
		if c.DiffHunk != "" {
			for _, line := range strings.Split(c.DiffHunk, "\n") {
				msgBody.WriteString("> ")
				msgBody.WriteString(line)
				msgBody.WriteString("\n")
			}
			msgBody.WriteString("\n")
		}
		msgBody.WriteString(c.Body)
		msgBody.WriteString("\n\n")
	}

	return g.git.SendEmail(from, subject, msgBody.String(), replyTo,
		EventHeader+": review")
}

func (g *ForgeToML) HandleCheckPending(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	for _, ps := range series.Patches {
		if err := g.pw.CreateCheck(
			ps.ID, "pending", event.CheckName, event.CheckURL, "",
		); err != nil {
			Errorf("failed to create pending check on patch %d: %v",
				ps.ID, err)
		}
	}
	return nil
}

func (g *ForgeToML) HandleCheckEvent(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	// post individual checks on each patch via patchwork API
	for _, run := range event.CheckRuns {
		state := mapCheckState(run.Status)
		for _, ps := range series.Patches {
			if err := g.pw.CreateCheck(
				ps.ID, state, run.Name, run.URL, run.Desc,
			); err != nil {
				Errorf("failed to create check %q on patch %d: %v",
					run.Name, ps.ID, err)
			}
		}
	}

	// also send an email to the mailing list
	replyTo := g.replyToMsgID(series, "")
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d", series.ID)
	}

	subject := fmt.Sprintf("Re: %s (CI: %s %s)",
		series.Name, event.CheckName, event.CheckStatus)

	var body strings.Builder
	if event.CheckDesc != "" {
		fmt.Fprintf(&body, "\n%s", event.CheckDesc)
	}

	return g.git.SendEmail(g.git.smtp.From, subject, body.String(), replyTo,
		EventHeader+": check")
}

func mapCheckState(ghConclusion string) string {
	switch ghConclusion {
	case "success":
		return "success"
	case "failure", "timed_out", "cancelled":
		return "fail"
	case "action_required":
		return "warning"
	default:
		return "pending"
	}
}

func (g *ForgeToML) replyToMsgID(series *patchwork.Series, filePath string) string {
	if filePath != "" {
		marker := "diff --git a/" + filePath + " b/" + filePath
		for _, ps := range series.Patches {
			patch, err := g.pw.GetPatch(ps.ID)
			if err != nil {
				continue
			}
			if strings.Contains(patch.Diff, marker) {
				return patch.MsgID
			}
		}
	}
	if series.CoverLetter != nil {
		return series.CoverLetter.MsgID
	}
	if len(series.Patches) > 0 {
		return series.Patches[0].MsgID
	}
	return ""
}

func (g *ForgeToML) HandlePullRequest(event *models.ForgeEvent) error {
	if err := g.git.EnsureMirror(); err != nil {
		return err
	}
	if err := g.git.Fetch(); err != nil {
		return err
	}

	workdir, err := os.MkdirTemp("", "pwforge-pr-*")
	if err != nil {
		return err
	}
	if err := g.git.AddWorktree(event.PRHead, workdir); err != nil {
		os.RemoveAll(workdir)
		return err
	}
	defer func() { _ = g.git.DelWorktree(workdir) }()

	version := 1
	var inReplyTo string
	if event.PRAction == "synchronize" {
		version, inReplyTo = g.nextVersionAndReplyTo(event)
	}

	prURL := g.forge.PRRef(event.PRNumber)
	return g.git.SendPatches(
		workdir, event.PRBase,
		event.PRTitle, sanitizePRBody(event.PRBody),
		event.PRBefore, inReplyTo, version,
		PRHeader+": "+prURL,
		BranchHeader+": "+event.PRHeadBranch,
	)
}

func (g *ForgeToML) nextVersionAndReplyTo(
	event *models.ForgeEvent,
) (int, string) {
	prRef := g.forge.PRRef(event.PRNumber)
	matches, err := g.pw.FindSeriesByMetadata(g.project, g.forge.MetaKeyPR(), prRef)
	if err != nil || len(matches) == 0 {
		return 1, ""
	}
	var first, latest *patchwork.Series
	for i := range matches {
		if first == nil || matches[i].Version < first.Version {
			first = &matches[i]
		}
		if latest == nil || matches[i].Version > latest.Version {
			latest = &matches[i]
		}
	}
	// always reply to v1's cover letter
	replyTo := ""
	if first.CoverLetter != nil {
		replyTo = first.CoverLetter.MsgID
	} else if len(first.Patches) > 0 {
		replyTo = first.Patches[0].MsgID
	}
	return latest.Version + 1, replyTo
}

const (
	PRHeader     = "X-PWForge-PR"
	BranchHeader = "X-PWForge-Branch"
)

var htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

var aiSectionHeaders = []string{
	"summary by coderabbit",
	"summary by copilot",
	"walkthrough",
	"generated by",
}

func sanitizePRBody(body string) string {
	// strip HTML comments (CodeRabbit config, pwforge markers, etc.)
	body = htmlCommentRe.ReplaceAllString(body, "")

	// strip sections starting with known AI headers
	lines := strings.Split(body, "\n")
	var result []string
	skip := false
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		// detect AI section headers (## Summary by CodeRabbit, etc.)
		if strings.HasPrefix(lower, "#") {
			heading := strings.TrimLeft(lower, "# ")
			for _, marker := range aiSectionHeaders {
				if strings.HasPrefix(heading, marker) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			skip = false
		}
		if !skip {
			result = append(result, line)
		}
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

func (g *ForgeToML) senderAddress(user models.ForgeUser) string {
	name, email := g.git.smtp.ParseFrom()
	switch {
	case user.Email != "" && user.Name != "":
		return fmt.Sprintf("%s <%s>", user.Name, user.Email)
	case user.Email != "":
		return fmt.Sprintf("%s <%s>", user.Login, user.Email)
	default:
		return fmt.Sprintf("%s (via %s) <%s>", user.Login, name, email)
	}
}
