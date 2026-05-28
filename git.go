// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
)

type GitMirror struct {
	conf  *config.GitConfig
	smtp  *config.SMTPConfig
	forge models.Forge
}

func CheckGitCommands(smtp *config.SMTPConfig) error {
	tmp, err := os.CreateTemp("", "pwforge-send-email-test-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.WriteString("Subject: foo\n\nbar"); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}

	args := []string{
		"send-email",
		"--dry-run",
		"--from=" + smtp.From,
		"--to=pwforge@users.noreply.github.com",
		"--smtp-server=" + smtp.Host,
		"--smtp-server-port=" + strconv.Itoa(smtp.Port),
		"--smtp-encryption=" + smtp.Encryption,
		"--confirm=never",
		"--no-validate",
		"--dry-run",
		"--envelope-sender=auto",
		"--8bit-encoding=UTF-8",
		"--suppress-cc=all",
	}
	if smtp.Auth != "" {
		args = append(args, "--smtp-auth="+smtp.Auth)
	}
	if smtp.Username != "" {
		args = append(args, "--smtp-user="+smtp.Username)
	}
	if smtp.Password != "" {
		args = append(args, "--smtp-pass="+smtp.Password)
	}
	args = append(args, tmp.Name())

	name, email := smtp.ParseFrom()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_COMMITTER_NAME="+name,
		"GIT_COMMITTER_EMAIL="+email,
		"GIT_AUTHOR_NAME="+name,
		"GIT_AUTHOR_EMAIL="+email,
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git send-email: %w\n%s", err, out)
	}

	return nil
}

func NewGitMirror(conf *config.GitConfig, smtp *config.SMTPConfig, forge models.Forge) *GitMirror {
	return &GitMirror{conf: conf, smtp: smtp, forge: forge}
}

func (m *GitMirror) withCredentials(fn func() error) error {
	credPath := filepath.Join(m.conf.MirrorPath, "pwforge-credentials")
	if err := m.forge.WriteCredentials(credPath); err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	defer os.Remove(credPath)
	return fn()
}

func (m *GitMirror) EnsureMirror() error {
	if _, err := os.Stat(filepath.Join(m.conf.MirrorPath, "HEAD")); err != nil {
		// create a temporary credentials file for the clone
		tmpCred, err := os.CreateTemp("", "pwforge-cred-*")
		if err != nil {
			return err
		}
		tmpCred.Close()
		defer os.Remove(tmpCred.Name())

		Infof("git: cloning mirror to %s", m.conf.MirrorPath)
		if err := os.MkdirAll(filepath.Dir(m.conf.MirrorPath), 0o755); err != nil {
			return err
		}
		if err := m.forge.WriteCredentials(tmpCred.Name()); err != nil {
			return fmt.Errorf("credentials: %w", err)
		}
		if err := m.git("clone", "--mirror",
			"-c", "credential.helper=store --file="+tmpCred.Name(),
			m.forge.RepoURL(), m.conf.MirrorPath); err != nil {
			return err
		}
	}

	// also fetch pull request heads
	if err := m.git("-C", m.conf.MirrorPath, "config", "--add",
		"remote.origin.fetch", "+refs/pull/*/head:refs/pull/*/head"); err != nil {
		return err
	}

	// configure credential helper for future operations
	credHelper := fmt.Sprintf("store --file=%s",
		filepath.Join(m.conf.MirrorPath, "pwforge-credentials"))

	fromName, fromEmail := m.smtp.ParseFrom()

	gitConfig := map[string]string{
		"credential.helper":            credHelper,
		"user.name":                    fromName,
		"user.email":                   fromEmail,
		"sendemail.to":                 m.smtp.To,
		"sendemail.smtpServer":         m.smtp.Host,
		"sendemail.smtpServerPort":     strconv.Itoa(m.smtp.Port),
		"sendemail.smtpEncryption":     m.smtp.Encryption,
		"sendemail.confirm":            "never",
		"sendemail.validate":           "false",
		"sendemail.chainreplyto":       "false",
		"sendemail.envelopesender":     "auto",
		"sendemail.assume8bitEncoding": "UTF-8",
		"sendemail.suppressCc":         "self",
		"format.subjectPrefix":         m.conf.SubjectPrefix,
		"format.coverFromDescription":  "subject",
	}
	if m.smtp.Auth != "" {
		gitConfig["sendemail.smtpAuth"] = m.smtp.Auth
	}
	if m.smtp.Username != "" {
		gitConfig["sendemail.smtpUser"] = m.smtp.Username
	}
	for k, v := range gitConfig {
		if err := m.git("-C", m.conf.MirrorPath, "config", k, v); err != nil {
			return err
		}
	}
	// write password separately to avoid logging it
	if m.smtp.Password != "" {
		if err := m.gitQuiet("-C", m.conf.MirrorPath, "config",
			"sendemail.smtpPass", m.smtp.Password); err != nil {
			return err
		}
	}
	return nil
}

func (m *GitMirror) Fetch() error {
	Infof("git: fetching mirror %s", m.conf.MirrorPath)
	return m.withCredentials(func() error {
		return m.git("-C", m.conf.MirrorPath, "fetch", "--all", "--prune")
	})
}

func (m *GitMirror) SendPatches(
	workdir, base, title, body, previousRef, inReplyTo string,
	version int, extraHeaders ...string,
) error {
	args := []string{
		"-C", workdir, "send-email", "--force",
		"--add-header=Reply-To: " + m.smtp.To,
	}
	for _, h := range extraHeaders {
		args = append(args, "--add-header="+h)
	}
	if version > 1 {
		args = append(args, fmt.Sprintf("-v%d", version))
	}
	if inReplyTo != "" {
		args = append(args, "--in-reply-to="+inReplyTo)
	}

	nCommits, _ := m.commitCount(workdir, base)
	if nCommits > 1 {
		descFile := filepath.Join(workdir, ".cover-description")
		desc := title + "\n\n" + body
		_ = os.WriteFile(descFile, []byte(desc), 0o644)
		args = append(args,
			"--cover-letter",
			"--cover-from-description=subject",
			"--description-file="+descFile)
	}

	if previousRef != "" && m.refExists(workdir, previousRef) {
		args = append(args, "--range-diff="+base+".."+previousRef)
	}

	args = append(args, base+"..HEAD")

	cmd := m.gitCmd(args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return nil
}

func (m *GitMirror) SendEmail(
	from, subject, body, inReplyTo string, extraHeaders ...string,
) error {
	Infof("sending email: %s -> %s (in-reply-to: %s): %s",
		from, m.smtp.To, inReplyTo, subject)

	tmpFile, err := os.CreateTemp("", "pwforge-email-*.eml")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())

	// write custom headers + body to the file
	// git send-email handles From/To/Subject/In-Reply-To via CLI args
	var msg strings.Builder
	for _, h := range extraHeaders {
		fmt.Fprintf(&msg, "%s\n", h)
	}
	fmt.Fprintf(&msg, "\n%s\n", strings.TrimSpace(body))

	if _, err := tmpFile.WriteString(msg.String()); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	args := []string{
		"-C", m.conf.MirrorPath,
		"send-email", "--force",
		"--from=" + from,
		"--subject=" + subject,
	}
	if inReplyTo != "" {
		args = append(args, "--in-reply-to="+strings.Trim(inReplyTo, "<>"))
	}
	args = append(args, tmpFile.Name())

	return m.git(args...)
}

func (m *GitMirror) refExists(workdir, ref string) bool {
	cmd := m.gitCmd("-C", workdir, "cat-file", "-t", ref)
	return cmd.Run() == nil
}

func (m *GitMirror) commitCount(workdir, base string) (int, error) {
	out, err := m.gitOutput("-C", workdir, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (m *GitMirror) AddWorktree(baseBranch, workdir string) error {
	return m.git("-C", m.conf.MirrorPath, "worktree", "add", "-fd", "--checkout",
		workdir, baseBranch)
}

func (m *GitMirror) DelWorktree(workdir string) error {
	return m.git("-C", m.conf.MirrorPath, "worktree", "remove", "-f", workdir)
}

func (m *GitMirror) ApplyMbox(workdir string, mbox []byte) error {
	mboxPath := filepath.Join(workdir, "series.mbox")
	Infof("git: writing mbox (%d bytes) to %s", len(mbox), mboxPath)
	if err := os.WriteFile(mboxPath, mbox, 0o644); err != nil {
		return fmt.Errorf("write mbox: %w", err)
	}
	return m.git("-C", workdir, "am", "-3", mboxPath)
}

func (m *GitMirror) Push(workdir, branch string) error {
	return m.withCredentials(func() error {
		return m.git("-C", workdir, "push", "-f",
			m.forge.RepoURL(), "HEAD:refs/heads/"+branch)
	})
}

func (m *GitMirror) gitCmd(args ...string) *exec.Cmd {
	Debugf("+ git %s", strings.Join(args, " "))
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	return cmd
}

func (m *GitMirror) gitQuiet(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git config: %w", err)
	}
	return nil
}

func (m *GitMirror) git(args ...string) error {
	cmd := m.gitCmd(args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return nil
}

func (m *GitMirror) gitOutput(args ...string) ([]byte, error) {
	cmd := m.gitCmd(args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return out, nil
}
