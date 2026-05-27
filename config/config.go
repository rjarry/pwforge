// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package config

import (
	"fmt"
	"net/mail"

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
	ForkOwner      string `ini:"fork-owner"`
	ForkRepo       string `ini:"fork-repo"`
	APIURL         string `ini:"api-url"`
}

type SMTPConfig struct {
	Host       string `ini:"host"`
	Port       int    `ini:"port"`
	Encryption string `ini:"encryption"`
	Auth       string `ini:"auth"`
	Username   string `ini:"username"`
	Password   string `ini:"password"`
	From       string `ini:"from"`
	To         string `ini:"to"`
}

func (s *SMTPConfig) ParseFrom() (name, email string) {
	addr, err := mail.ParseAddress(s.From)
	if err != nil {
		return "Pwforge", s.From
	}
	if addr.Name == "" {
		addr.Name = "Pwforge"
	}
	return addr.Name, addr.Address
}

type GitConfig struct {
	MirrorPath    string `ini:"mirror-path"`
	BranchPrefix  string `ini:"branch-prefix"`
	SubjectPrefix string `ini:"subject-prefix"`
}

func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Listen: ":8080",
		Forge:  "github",
		GitHub: GitHubConfig{},
		SMTP: SMTPConfig{
			Port:       587,
			Encryption: "tls",
		},
		Git: GitConfig{
			MirrorPath:    "/var/cache/pwforge/repo.git",
			BranchPrefix:  "pwforge",
			SubjectPrefix: "PATCH",
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
