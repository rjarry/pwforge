// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package sync

import (
	"fmt"
	"log"
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

		log.Printf("git: cloning mirror to %s", m.conf.MirrorPath)
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
		"sendemail.from":               m.smtp.From,
		"sendemail.smtpServer":         m.smtp.Host,
		"sendemail.smtpServerPort":     strconv.Itoa(m.smtp.Port),
		"sendemail.smtpEncryption":     m.smtp.Encryption,
		"sendemail.confirm":            "never",
		"sendemail.validate":           "false",
		"sendemail.chainreplyto":       "false",
		"sendemail.envelopesender":     "auto",
		"sendemail.assume8bitEncoding": "UTF-8",
		"sendemail.suppressCc":         "self",
		"sendemail.suppressFrom":       "true",
		"format.subjectPrefix":         m.conf.SubjectPrefix,
		"format.coverFromDescription":  "subject",
	}
	if m.smtp.Username != "" {
		gitConfig["sendemail.smtpUser"] = m.smtp.Username
		gitConfig["sendemail.smtpPass"] = m.smtp.Password
	}
	for k, v := range gitConfig {
		if err := m.git("-C", m.conf.MirrorPath, "config", k, v); err != nil {
			return err
		}
	}
	return nil
}

func (m *GitMirror) Fetch() error {
	log.Printf("git: fetching mirror %s", m.conf.MirrorPath)
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

	if previousRef != "" {
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
	log.Printf("git: writing mbox (%d bytes) to %s", len(mbox), mboxPath)
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
	log.Printf("+ git %s", strings.Join(args, " "))
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	return cmd
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
