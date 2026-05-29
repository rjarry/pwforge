// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package github

import (
	"context"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v84/github"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

type TokenSource struct {
	transport *ghinstallation.Transport
	token     string
}

func NewTokenSourcePAT(token string) *TokenSource {
	return &TokenSource{token: token}
}

func NewTokenSourceApp(
	appID, installationID int64, keyPath string,
) (*TokenSource, error) {
	if installationID == 0 {
		id, err := discoverInstallation(appID, keyPath)
		if err != nil {
			return nil, err
		}
		installationID = id
	}
	tr, err := ghinstallation.NewKeyFromFile(
		http.DefaultTransport, appID, installationID, keyPath,
	)
	if err != nil {
		return nil, fmt.Errorf("github app transport: %w", err)
	}
	return &TokenSource{transport: tr}, nil
}

func discoverInstallation(appID int64, keyPath string) (int64, error) {
	appTr, err := ghinstallation.NewAppsTransportKeyFromFile(
		http.DefaultTransport, appID, keyPath,
	)
	if err != nil {
		return 0, fmt.Errorf("github app JWT transport: %w", err)
	}
	client := gh.NewClient(&http.Client{Transport: appTr})
	installations, _, err := client.Apps.ListInstallations(
		context.Background(), nil,
	)
	if err != nil {
		return 0, fmt.Errorf("list installations: %w", err)
	}
	if len(installations) == 0 {
		return 0, fmt.Errorf(
			"no installations found for app %d: "+
				"install the app on your repos first", appID)
	}
	return installations[0].GetID(), nil
}

func (ts *TokenSource) Token() (string, error) {
	if ts.transport != nil {
		return ts.transport.Token(context.Background())
	}
	return ts.token, nil
}

func (ts *TokenSource) Transport() http.RoundTripper {
	if ts.transport != nil {
		return ts.transport
	}
	return &patTransport{token: ts.token}
}

type patTransport struct {
	token string
}

func (t *patTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}
