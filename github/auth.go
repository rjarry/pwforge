// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package github

import (
	"context"
	"fmt"
	"net/http"

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
	tr, err := ghinstallation.NewKeyFromFile(
		http.DefaultTransport, appID, installationID, keyPath,
	)
	if err != nil {
		return nil, fmt.Errorf("github app transport: %w", err)
	}
	return &TokenSource{transport: tr}, nil
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
