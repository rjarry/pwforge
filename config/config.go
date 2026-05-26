// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package config

import (
	"fmt"

	"gopkg.in/ini.v1"
)

type Config struct {
	Listen    string          `ini:"listen"`
	Forge     string          `ini:"forge"`
	Patchwork PatchworkConfig `ini:"patchwork"`
	GitHub    GitHubConfig    `ini:"github"`
	SMTP      SMTPConfig      `ini:"smtp"`
	Git       GitConfig       `ini:"git"`
}

type PatchworkConfig struct {
	URL           string `ini:"url"`
	Token         string `ini:"token"`
	WebhookSecret string `ini:"webhook-secret"`
	Project       string `ini:"project"`
}

type GitHubConfig struct {
	Token          string `ini:"token"`
	AppID          int64  `ini:"app-id"`
	InstallationID int64  `ini:"installation-id"`
	PrivateKeyFile string `ini:"private-key-file"`
	WebhookSecret  string `ini:"webhook-secret"`
	Owner          string `ini:"owner"`
	Repo           string `ini:"repo"`
	APIURL         string `ini:"api-url"`
}

type SMTPConfig struct {
	Host     string `ini:"host"`
	Port     int    `ini:"port"`
	Username string `ini:"username"`
	Password string `ini:"password"`
	From     string `ini:"from"`
	To       string `ini:"to"`
}

type GitConfig struct {
	MirrorPath     string `ini:"mirror-path"`
	BranchPrefix   string `ini:"branch-prefix"`
	CommitterName  string `ini:"committer-name"`
	CommitterEmail string `ini:"committer-email"`
}

func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Listen: ":8080",
		Forge:  "github",
		GitHub: GitHubConfig{},
		SMTP: SMTPConfig{
			Port: 587,
		},
		Git: GitConfig{
			MirrorPath:     "/var/cache/pwforge/repo.git",
			BranchPrefix:   "pwforge",
			CommitterName:  "pwforge",
			CommitterEmail: "pwforge@local",
		},
	}

	if path != "" {
		f, err := ini.Load(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := f.MapTo(cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}

	return cfg, nil
}
