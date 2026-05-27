// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package sync

import (
	"fmt"
	"log"
	"net/smtp"
	"os"
	"regexp"
	"strings"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
	"github.com/rjarry/pwforge/patchwork"
)

type ForgeToML struct {
	pw   *patchwork.Client
	smtp *config.SMTPConfig
}

func NewForgeToML(pw *patchwork.Client, smtp *config.SMTPConfig) *ForgeToML {
	return &ForgeToML{pw: pw, smtp: smtp}
}

func (g *ForgeToML) HandleIssueComment(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	if series.CoverLetter == nil {
		log.Printf("series %d has no cover letter, using first patch", series.ID)
	}

	replyTo := g.replyToMsgID(series, "")
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d", series.ID)
	}

	from := senderAddress(event.Author, g.smtp.From)
	subject := "Re: " + series.Name

	return g.sendEmail(from, subject, event.Body, replyTo)
}

func (g *ForgeToML) HandleReviewComment(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	replyTo := g.replyToMsgID(series, event.Path)
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d file %s",
			series.ID, event.Path)
	}

	from := senderAddress(event.Author, g.smtp.From)
	subject := "Re: " + series.Name

	var msgBody strings.Builder
	if event.DiffHunk != "" {
		for _, line := range strings.Split(event.DiffHunk, "\n") {
			msgBody.WriteString("> ")
			msgBody.WriteString(line)
			msgBody.WriteString("\n")
		}
		msgBody.WriteString("\n")
	}
	msgBody.WriteString(event.Body)

	return g.sendEmail(from, subject, msgBody.String(), replyTo)
}

func (g *ForgeToML) HandleCheckPending(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	for _, ps := range series.Patches {
		if err := g.pw.CreateCheck(
			ps.ID, "pending", event.CheckName, event.CheckURL, "",
		); err != nil {
			log.Printf("failed to create pending check on patch %d: %v",
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
				log.Printf("failed to create check %q on patch %d: %v",
					run.Name, ps.ID, err)
			}
		}
	}

	// also send an email to the mailing list
	replyTo := g.replyToMsgID(series, "")
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d", series.ID)
	}

	subject := "Re: " + series.Name

	var body strings.Builder
	fmt.Fprintf(&body, "CI: %s %s\n", event.CheckName, event.CheckStatus)
	if event.CheckDesc != "" {
		fmt.Fprintf(&body, "\n%s", event.CheckDesc)
	}

	return g.sendEmail(g.smtp.From, subject, body.String(), replyTo)
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

func (g *ForgeToML) HandlePullRequest(
	event *models.ForgeEvent, git *GitMirror, forge models.Forge,
	project string,
) error {
	if err := git.EnsureMirror(); err != nil {
		return err
	}
	if err := git.Fetch(); err != nil {
		return err
	}

	workdir, err := os.MkdirTemp("", "pwforge-pr-*")
	if err != nil {
		return err
	}
	if err := git.AddWorktree(event.PRHead, workdir); err != nil {
		os.RemoveAll(workdir)
		return err
	}
	defer func() { _ = git.DelWorktree(workdir) }()

	version := 1
	var inReplyTo string
	if event.PRAction == "synchronize" {
		version, inReplyTo = g.nextVersionAndReplyTo(event, forge, project)
	}

	prURL := forge.PRRef(event.PRNumber)
	return git.SendPatches(
		workdir, event.PRBase,
		event.PRTitle, sanitizePRBody(event.PRBody),
		event.PRBefore, inReplyTo, version,
		PRHeader+": "+prURL,
		BranchHeader+": "+event.PRHeadBranch,
	)
}

func (g *ForgeToML) nextVersionAndReplyTo(
	event *models.ForgeEvent, forge models.Forge, project string,
) (int, string) {
	prRef := forge.PRRef(event.PRNumber)
	matches, err := g.pw.FindSeriesByMetadata(project, forge.MetaKeyPR(), prRef)
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

const PRHeader = "X-PWForge-PR"
const BranchHeader = "X-PWForge-Branch"

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

func senderAddress(user models.ForgeUser, fallbackFrom string) string {
	switch {
	case user.Email != "" && user.Name != "":
		return fmt.Sprintf("%s <%s>", user.Name, user.Email)
	case user.Email != "":
		return fmt.Sprintf("%s <%s>", user.Login, user.Email)
	default:
		return fmt.Sprintf("%s (via pwforge) <%s>", user.Login, fallbackFrom)
	}
}

func (g *ForgeToML) sendEmail(from, subject, body, inReplyTo string) error {
	msg := &strings.Builder{}
	fmt.Fprintf(msg, "From: %s\r\n", from)
	fmt.Fprintf(msg, "To: %s\r\n", g.smtp.To)
	fmt.Fprintf(msg, "Subject: %s\r\n", subject)
	if inReplyTo != "" {
		if !strings.HasPrefix(inReplyTo, "<") {
			inReplyTo = "<" + inReplyTo + ">"
		}
		fmt.Fprintf(msg, "In-Reply-To: %s\r\n", inReplyTo)
		fmt.Fprintf(msg, "References: %s\r\n", inReplyTo)
	}
	fmt.Fprintf(msg, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(msg, "\r\n")
	fmt.Fprintf(msg, "%s\r\n", body)

	addr := fmt.Sprintf("%s:%d", g.smtp.Host, g.smtp.Port)

	var auth smtp.Auth
	if g.smtp.Username != "" {
		auth = smtp.PlainAuth("", g.smtp.Username, g.smtp.Password, g.smtp.Host)
	}

	log.Printf("sending email: %s -> %s (reply-to: %s)", from, g.smtp.To, inReplyTo)

	return smtp.SendMail(addr, auth, g.smtp.From, []string{g.smtp.To}, []byte(msg.String()))
}
