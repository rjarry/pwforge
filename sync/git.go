// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Robin Jarry

package sync

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rjarry/pwforge/config"
	"github.com/rjarry/pwforge/models"
)

type GitMirror struct {
	conf  *config.GitConfig
	forge models.Forge
}

func NewGitMirror(conf *config.GitConfig, forge models.Forge) *GitMirror {
	return &GitMirror{conf: conf, forge: forge}
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
	if _, err := os.Stat(filepath.Join(m.conf.MirrorPath, "HEAD")); err == nil {
		return nil
	}
	log.Printf("git: cloning mirror to %s", m.conf.MirrorPath)
	if err := os.MkdirAll(filepath.Dir(m.conf.MirrorPath), 0o755); err != nil {
		return err
	}
	// create a temporary credentials file for the clone
	tmpCred, err := os.CreateTemp("", "pwforge-cred-*")
	if err != nil {
		return err
	}
	tmpCred.Close()
	defer os.Remove(tmpCred.Name())
	if err := m.forge.WriteCredentials(tmpCred.Name()); err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	if err := m.git("clone", "--mirror",
		"-c", "credential.helper=store --file="+tmpCred.Name(),
		m.forge.RepoURL(), m.conf.MirrorPath); err != nil {
		return err
	}
	// configure credential helper for future operations
	return m.git("-C", m.conf.MirrorPath, "config",
		"credential.helper", "store --file="+
			filepath.Join(m.conf.MirrorPath, "pwforge-credentials"))
}

func (m *GitMirror) Fetch() error {
	log.Printf("git: fetching mirror %s", m.conf.MirrorPath)
	return m.withCredentials(func() error {
		return m.git("-C", m.conf.MirrorPath, "fetch", "--all", "--prune")
	})
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

func (m *GitMirror) git(args ...string) error {
	line := "git " + strings.Join(args, " ")
	log.Printf("+ %s", line)
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_COMMITTER_NAME="+m.conf.CommitterName,
		"GIT_COMMITTER_EMAIL="+m.conf.CommitterEmail,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", line, err)
	}
	return nil
}
