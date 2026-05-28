// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package config

import (
	"fmt"
	"net/mail"
	"strings"

	"gopkg.in/ini.v1"
)

type Config struct {
	Path      string          `ini:"-"`
	Listen    string          `ini:"listen"`
	BaseURL   string          `ini:"base-url"`
	Forge     string          `ini:"forge"`
	QueueSize int             `ini:"queue-size"`
	Patchwork PatchworkConfig `ini:"patchwork"`
	GitHub    GitHubConfig    `ini:"github"`
	SMTP      SMTPConfig      `ini:"smtp"`
	Git       GitConfig       `ini:"git"`
	Sync      SyncConfig      `ini:"sync"`
	Projects  map[string]*ProjectConfig
}

type SyncConfig struct {
	MLToForge bool `ini:"ml-to-forge"`
	ForgeToML bool `ini:"forge-to-ml"`
}

type PatchworkConfig struct {
	URL           string `ini:"url"`
	Token         string `ini:"token"`
	WebhookSecret string `ini:"webhook-secret"`
	Project       string `ini:"project"`
	CacheTTL      int    `ini:"cache-ttl"`
}

type GitHubConfig struct {
	Token          string `ini:"token"`
	AppID          int64  `ini:"app-id"`
	InstallationID int64  `ini:"installation-id"`
	PrivateKeyFile string `ini:"private-key-file"`
	WebhookSecret  string `ini:"webhook-secret"`
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

type ProjectConfig struct {
	Owner         string `ini:"owner"`
	Repo          string `ini:"repo"`
	ForkOwner     string `ini:"fork-owner"`
	ForkRepo      string `ini:"fork-repo"`
	SubjectPrefix string `ini:"subject-prefix"`
}

func LoadConfig(path string) (*Config, error) {
	cfg := &Config{
		Listen:    ":8080",
		Forge:     "github",
		QueueSize: 10,
		GitHub:    GitHubConfig{},
		SMTP: SMTPConfig{
			Port:       587,
			Encryption: "tls",
		},
		Sync: SyncConfig{
			MLToForge: true,
			ForgeToML: true,
		},
		Git: GitConfig{
			MirrorPath:    "/var/cache/pwforge",
			BranchPrefix:  "pwforge",
			SubjectPrefix: "PATCH",
		},
		Patchwork: PatchworkConfig{
			CacheTTL: 600,
		},
		Projects: make(map[string]*ProjectConfig),
	}

	cfg.Path = path

	if path != "" {
		f, err := ini.Load(path)
		if err != nil {
			return nil, fmt.Errorf("read config: %w", err)
		}
		if err := f.MapTo(cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}

		// parse [project "linkname"] sections
		for _, sec := range f.Sections() {
			name := sec.Name()
			if !strings.HasPrefix(name, "project ") {
				continue
			}
			linkName := strings.Trim(
				strings.TrimPrefix(name, "project"), " \"")
			if linkName == "" {
				continue
			}
			p := &ProjectConfig{}
			if err := sec.MapTo(p); err != nil {
				return nil, fmt.Errorf(
					"parse project %q: %w", linkName, err)
			}
			cfg.Projects[linkName] = p
		}
	}

	return cfg, nil
}
