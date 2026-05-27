// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Robin Jarry <robin@jarry.cc>

package patchwork

import (
	"encoding/json"
	"time"
)

type FlexTime struct {
	time.Time
}

func (t *FlexTime) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	} {
		if parsed, err := time.Parse(layout, s); err == nil {
			t.Time = parsed
			return nil
		}
	}
	return &time.ParseError{
		Value:   s,
		Message: "unrecognized time format",
	}
}

type Person struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type User struct {
	ID        int    `json:"id"`
	URL       string `json:"url"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Email     string `json:"email"`
}

type Project struct {
	ID       int    `json:"id"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	LinkName string `json:"link_name"`
	ListID   string `json:"list_id"`
}

type PatchSummary struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	MsgID string `json:"msgid"`
	Date  string `json:"date"`
	Name  string `json:"name"`
	Mbox  string `json:"mbox"`
}

type CoverSummary struct {
	ID    int    `json:"id"`
	URL   string `json:"url"`
	MsgID string `json:"msgid"`
	Date  string `json:"date"`
	Name  string `json:"name"`
	Mbox  string `json:"mbox"`
}

type Series struct {
	ID             int                    `json:"id"`
	URL            string                 `json:"url"`
	WebURL         string                 `json:"web_url"`
	Project        Project                `json:"project"`
	Name           string                 `json:"name"`
	Date           FlexTime               `json:"date"`
	Submitter      Person                 `json:"submitter"`
	Version        int                    `json:"version"`
	Total          int                    `json:"total"`
	ReceivedTotal  int                    `json:"received_total"`
	ReceivedAll    bool                   `json:"received_all"`
	Mbox           string                 `json:"mbox"`
	CoverLetter    *CoverSummary          `json:"cover_letter"`
	Patches        []PatchSummary         `json:"patches"`
	PreviousSeries *string                `json:"previous_series"`
	Metadata       map[string]interface{} `json:"metadata"`
}

type Patch struct {
	ID        int                    `json:"id"`
	URL       string                 `json:"url"`
	WebURL    string                 `json:"web_url"`
	Project   Project                `json:"project"`
	MsgID     string                 `json:"msgid"`
	Date      FlexTime               `json:"date"`
	Name      string                 `json:"name"`
	Submitter Person                 `json:"submitter"`
	State     string                 `json:"state"`
	Mbox      string                 `json:"mbox"`
	Content   string                 `json:"content"`
	Series    []PatchSummary         `json:"series"`
	Diff      string                 `json:"diff"`
	Headers   map[string]interface{} `json:"headers"`
	Prefixes  []string               `json:"prefixes"`
	Metadata  map[string]interface{} `json:"metadata"`
}

type Cover struct {
	ID        int                    `json:"id"`
	URL       string                 `json:"url"`
	WebURL    string                 `json:"web_url"`
	Project   Project                `json:"project"`
	MsgID     string                 `json:"msgid"`
	Date      FlexTime               `json:"date"`
	Name      string                 `json:"name"`
	Submitter Person                 `json:"submitter"`
	Mbox      string                 `json:"mbox"`
	Series    []PatchSummary         `json:"series"`
	Content   string                 `json:"content"`
	Headers   map[string]interface{} `json:"headers"`
}

type Comment struct {
	ID        int                    `json:"id"`
	URL       string                 `json:"url"`
	MsgID     string                 `json:"msgid"`
	Date      FlexTime               `json:"date"`
	Subject   string                 `json:"subject"`
	Submitter Person                 `json:"submitter"`
	Content   string                 `json:"content"`
	Headers   map[string]interface{} `json:"headers"`
}

type EventPayload struct {
	Series *PatchSummary `json:"series"`
	Patch  *PatchSummary `json:"patch"`
	Cover  *CoverSummary `json:"cover"`
}

type Event struct {
	ID       int          `json:"id"`
	Category string       `json:"category"`
	Project  Project      `json:"project"`
	Date     FlexTime     `json:"date"`
	Actor    *User        `json:"actor"`
	Payload  EventPayload `json:"payload"`
}
