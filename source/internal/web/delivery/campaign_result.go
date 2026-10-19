// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
)

const campaignResultMarker = "ANVIL_CAMPAIGN_RESULT:"

// CampaignCaseOutcome is one case line from a Worker trailer.
type CampaignCaseOutcome struct {
	ID     string `json:"id"`
	Status string `json:"status"` // passed|failed
}

// CampaignResultTrailer is the structured Worker trailer payload.
type CampaignResultTrailer struct {
	Cases []CampaignCaseOutcome `json:"cases"`
}

// ParseCampaignResult extracts ANVIL_CAMPAIGN_RESULT JSON from job result text.
func ParseCampaignResult(result string) (CampaignResultTrailer, bool) {
	var out CampaignResultTrailer
	idx := strings.Index(result, campaignResultMarker)
	if idx < 0 {
		// also accept lowercase marker
		idx = strings.Index(strings.ToUpper(result), campaignResultMarker)
		if idx < 0 {
			return out, false
		}
	}
	rest := strings.TrimSpace(result[idx+len(campaignResultMarker):])
	// Take first JSON object.
	start := strings.Index(rest, "{")
	if start < 0 {
		return out, false
	}
	rest = rest[start:]
	dec := json.NewDecoder(strings.NewReader(rest))
	if err := dec.Decode(&out); err != nil {
		return CampaignResultTrailer{}, false
	}
	if len(out.Cases) == 0 {
		return out, false
	}
	return out, true
}

// ApplyCampaignRunResult applies parsed case outcomes and opens bugs for failures.
func (s *Store) ApplyCampaignRunResult(engID string, major, minor int, trail CampaignResultTrailer) (RunTestCasesResult, error) {
	var out RunTestCasesResult
	engID = strings.TrimSpace(engID)
	if engID == "" {
		return out, fmt.Errorf("eng_req_id required")
	}
	existingBugs, err := s.listBuglistForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	openTitles := map[string]bool{}
	for _, b := range existingBugs {
		if b.Status == "open" || b.Status == "fixing" {
			openTitles[b.Title] = true
		}
	}
	cases, err := s.listTestCasesForBind(engID, major, minor)
	if err != nil {
		return out, err
	}
	byID := map[string]TestCase{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	seen := map[string]bool{}
	for _, row := range trail.Cases {
		id := strings.TrimSpace(row.ID)
		st := strings.ToLower(strings.TrimSpace(row.Status))
		if id == "" || byID[id].ID == "" {
			continue
		}
		if st != "passed" && st != "failed" {
			continue
		}
		_, err := s.db.Exec(`UPDATE delivery_test_cases SET status=? WHERE id=?`, st, id)
		if err != nil {
			return out, err
		}
		c := byID[id]
		c.Status = st
		out.Cases = append(out.Cases, c)
		seen[id] = true
		if st == "failed" {
			bugTitle := "Fail: " + c.Title
			if openTitles[bugTitle] {
				continue
			}
			b, err := s.CreateBuglistItem(CreateBuglistInput{
				EngReqID: engID,
				Major:    major,
				Minor:    minor,
				Title:    bugTitle,
				Body:     "Campaign run failed for case: " + c.Title + "\n" + c.Body,
			})
			if err != nil {
				return out, err
			}
			openTitles[bugTitle] = true
			out.Bugs = append(out.Bugs, b)
		}
	}
	// Cases not mentioned keep prior status but still appear in result list.
	for _, c := range cases {
		if seen[c.ID] {
			continue
		}
		out.Cases = append(out.Cases, c)
	}
	if out.Bugs == nil {
		out.Bugs = []BuglistItem{}
	}
	return out, nil
}
