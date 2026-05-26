// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package sync

import (
	"fmt"
	"log"
	"net/smtp"
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

func (g *ForgeToML) HandleCheckEvent(
	event *models.ForgeEvent, series *patchwork.Series,
) error {
	replyTo := g.replyToMsgID(series, "")
	if replyTo == "" {
		return fmt.Errorf("no message-id found for series %d", series.ID)
	}

	subject := "Re: " + series.Name

	var body strings.Builder
	fmt.Fprintf(&body, "CI check %q %s", event.CheckName, event.CheckStatus)
	if event.CheckURL != "" {
		fmt.Fprintf(&body, ": %s", event.CheckURL)
	}
	body.WriteString("\n")
	if event.CheckDesc != "" {
		fmt.Fprintf(&body, "\n%s\n", event.CheckDesc)
	}

	return g.sendEmail(g.smtp.From, subject, body.String(), replyTo)
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
		fmt.Fprintf(msg, "In-Reply-To: <%s>\r\n", inReplyTo)
		fmt.Fprintf(msg, "References: <%s>\r\n", inReplyTo)
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
