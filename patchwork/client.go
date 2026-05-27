// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package patchwork

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		},
	}
}

func (c *Client) apiURL(path string) string {
	return fmt.Sprintf("%s/api/1.5/%s", c.baseURL, path)
}

func (c *Client) doJSON(method, path string, body io.Reader, result interface{}) error {
	url := c.apiURL(path)
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		// drain remaining body to allow connection reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API %s %s: %d: %s", method, path, resp.StatusCode, data)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) GetSeries(id int) (*Series, error) {
	var s Series
	err := c.doJSON("GET", fmt.Sprintf("series/%d/", id), nil, &s)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Client) GetPatch(id int) (*Patch, error) {
	var p Patch
	err := c.doJSON("GET", fmt.Sprintf("patches/%d/", id), nil, &p)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) GetCover(id int) (*Cover, error) {
	var cv Cover
	err := c.doJSON("GET", fmt.Sprintf("covers/%d/", id), nil, &cv)
	if err != nil {
		return nil, err
	}
	return &cv, nil
}

func (c *Client) GetPatchComments(patchID int) ([]Comment, error) {
	var comments []Comment
	err := c.doJSON("GET", fmt.Sprintf("patches/%d/comments/", patchID), nil, &comments)
	if err != nil {
		return nil, err
	}
	return comments, nil
}

func (c *Client) GetCoverComments(coverID int) ([]Comment, error) {
	var comments []Comment
	err := c.doJSON("GET", fmt.Sprintf("covers/%d/comments/", coverID), nil, &comments)
	if err != nil {
		return nil, err
	}
	return comments, nil
}

func (c *Client) UpdateSeriesMetadata(seriesID int, metadata map[string]interface{}) error {
	body, err := json.Marshal(map[string]interface{}{
		"metadata": metadata,
	})
	if err != nil {
		return err
	}

	return c.doJSON("PATCH", fmt.Sprintf("series/%d/", seriesID),
		bytes.NewReader(body), nil)
}

func (c *Client) CreateCheck(patchID int, state, context, targetURL, description string) error {
	check := map[string]string{
		"state":   state,
		"context": context,
	}
	if targetURL != "" {
		check["target_url"] = targetURL
	}
	if description != "" {
		check["description"] = description
	}
	body, err := json.Marshal(check)
	if err != nil {
		return err
	}
	return c.doJSON("POST", fmt.Sprintf("patches/%d/checks/", patchID),
		bytes.NewReader(body), nil)
}

func (c *Client) FindSeriesByMetadata(project, key, value string) ([]Series, error) {
	var series []Series
	path := fmt.Sprintf("series/?project=%s&metadata_key=%s&metadata_value=%s&order=-id&per_page=1",
		url.QueryEscape(project),
		url.QueryEscape(key),
		url.QueryEscape(value))
	err := c.doJSON("GET", path, nil, &series)
	if err != nil {
		return nil, err
	}
	return series, nil
}

func (c *Client) ListSeries(project string) ([]Series, error) {
	var series []Series
	err := c.doJSON("GET", fmt.Sprintf("series/?project=%s&per_page=250", project), nil, &series)
	if err != nil {
		return nil, err
	}
	return series, nil
}

func (c *Client) GetSeriesMbox(id int) ([]byte, error) {
	url := fmt.Sprintf("%s/series/%d/mbox/", c.baseURL, id)

	var data []byte
	for attempt := range 5 {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Token "+c.token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, fmt.Errorf("mbox %d: status %d", id, resp.StatusCode)
		}

		data, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > 0 {
			break
		}
		log.Printf("mbox %d: empty response (content-length=%d, status=%d), retrying (%d/5)",
			id, resp.ContentLength, resp.StatusCode, attempt+1)
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("mbox %d: empty after retries", id)
	}
	return data, nil
}
