// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/rjarry/pwforge/config"

	"gopkg.in/ini.v1"
)

func SetupHandler(conf *config.Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) {
		handleSetupPage(w, r, conf)
	})
	mux.HandleFunc("GET /setup/callback", func(w http.ResponseWriter, r *http.Request) {
		handleSetupCallback(w, r, conf)
	})
	return mux
}

func handleSetupPage(w http.ResponseWriter, r *http.Request, conf *config.Config) {
	if conf.GitHub.AppID != 0 {
		fmt.Fprintf(w, setupDonePage, conf.GitHub.AppID)
		return
	}

	baseURL := strings.TrimRight(conf.BaseURL, "/")
	if baseURL == "" {
		scheme := "https"
		if r.TLS == nil && !strings.HasPrefix(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "http"
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, r.Host)
	}

	manifest := map[string]any{
		"name": "pwforge",
		"url":  baseURL,
		"hook_attributes": map[string]any{
			"url":    baseURL + "/forge",
			"active": true,
		},
		"redirect_url": baseURL + "/setup/callback",
		"public":       false,
		"default_permissions": map[string]string{
			"contents":      "read",
			"pull_requests": "write",
			"issues":        "write",
			"checks":        "read",
		},
		"default_events": []string{
			"pull_request",
			"pull_request_review",
			"issue_comment",
			"check_suite",
			"check_run",
		},
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	apiBase := "https://github.com"
	if conf.GitHub.APIURL != "" {
		apiBase = strings.TrimSuffix(conf.GitHub.APIURL, "/api/v3")
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, setupPage, apiBase, manifestJSON)
}

func handleSetupCallback(w http.ResponseWriter, r *http.Request, conf *config.Config) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}

	apiURL := "https://api.github.com"
	if conf.GitHub.APIURL != "" {
		apiURL = conf.GitHub.APIURL
	}

	resp, err := http.Post(
		fmt.Sprintf("%s/app-manifests/%s/conversions", apiURL, code),
		"application/json", nil,
	)
	if err != nil {
		http.Error(w,
			fmt.Sprintf("exchange code: %v", err),
			http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		http.Error(w,
			fmt.Sprintf("GitHub API error: %s", body),
			http.StatusBadGateway)
		return
	}

	var result struct {
		ID            int64  `json:"id"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
		HTMLURL       string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		http.Error(w,
			fmt.Sprintf("parse response: %v", err),
			http.StatusInternalServerError)
		return
	}

	if err := writeCredentials(conf, result.ID, result.PEM, result.WebhookSecret); err != nil {
		http.Error(w,
			fmt.Sprintf("write credentials: %v", err),
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, setupSuccessPage,
		result.HTMLURL, result.HTMLURL+"/installations/new")
}

func writeCredentials(conf *config.Config, appID int64, pem, webhookSecret string) error {
	if conf.Path == "" {
		return fmt.Errorf("no config file path, cannot save credentials")
	}

	configDir := filepath.Dir(conf.Path)
	pemPath := filepath.Join(configDir, "github-app.pem")

	if err := os.WriteFile(pemPath, []byte(pem), 0o600); err != nil {
		return fmt.Errorf("write PEM: %w", err)
	}

	f, err := ini.Load(conf.Path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	sec := f.Section("github")
	sec.Key("app-id").SetValue(fmt.Sprintf("%d", appID))
	sec.Key("private-key-file").SetValue(pemPath)
	sec.Key("webhook-secret").SetValue(webhookSecret)
	sec.DeleteKey("token")

	if err := f.SaveTo(conf.Path); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}

const setupPage = `<!DOCTYPE html>
<html>
<head><title>pwforge setup</title></head>
<body>
<h2>pwforge GitHub App Setup</h2>
<p>Click the button below to register a GitHub App for this pwforge instance.
You will be redirected to GitHub to confirm the app name and permissions.</p>
<form action="%s/settings/apps/new" method="post">
<input type="hidden" name="manifest" value='%s'>
<button type="submit">Register GitHub App</button>
</form>
</body>
</html>`

const setupDonePage = `<!DOCTYPE html>
<html>
<head><title>pwforge setup</title></head>
<body>
<h2>pwforge GitHub App Setup</h2>
<p>A GitHub App is already configured (app-id: %d).</p>
</body>
</html>`

const setupSuccessPage = `<!DOCTYPE html>
<html>
<head><title>pwforge setup</title></head>
<body>
<h2>GitHub App Created</h2>
<p>The GitHub App has been registered and credentials saved to disk.</p>
<p>Next steps:</p>
<ol>
<li>
  <a href="%s">View the app on GitHub</a> and verify the settings.
</li>
<li>
  <a href="%s">Install the app</a> on your organization or repositories.
</li>
<li>Restart pwforge to pick up the new credentials.</li>
</ol>
</body>
</html>`
