// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"wardlock.nz/apiclient"
)

const configFile = "wardlock.json"

// projectConfig is wardlock.json in the directory the CLI runs from
// (docs/SPEC.md "wardlock.json and .wardlockignore"). Every field is
// optional and flags override them.
type projectConfig struct {
	Server string `json:"server,omitempty"`
	Site   string `json:"site,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Spa    *bool  `json:"spa,omitempty"`
	// ImmutableAssets is "immutableAssets": long-term caching for hashed
	// files under /assets/.
	ImmutableAssets *bool `json:"immutableAssets,omitempty"`
	// Path is "path": deploy into this prefix of the site, keeping the
	// rest (C1.13).
	Path string `json:"path,omitempty"`
	// Group is "group": file the deployed site in this existing group
	// (V5.15).
	Group string `json:"group,omitempty"`
	// Tags is "tags": add these existing tags to the deployed site.
	Tags []string `json:"tags,omitempty"`
	// Redirects is "redirects": the deploy's redirect rules, in order
	// (C1.17). Left out, a path deploy keeps its base's; [] for none.
	Redirects *[]apiclient.Redirect `json:"redirects,omitempty"`
	// Headers is "headers": the deploy's response header rules, in order,
	// left out or [] as for Redirects.
	Headers *[]apiclient.HeaderRule `json:"headers,omitempty"`
}

// loadConfig reads wardlock.json from dir. A missing file is an empty
// config; an unreadable or invalid one, including an unknown field, is a
// usage error naming the file.
func loadConfig(dir string) (projectConfig, error) {
	path := filepath.Join(dir, configFile)
	b, err := readInDir(dir, configFile)
	if errors.Is(err, fs.ErrNotExist) {
		return projectConfig{}, nil
	}
	if err != nil {
		return projectConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := parseConfig(b)
	if err != nil {
		return projectConfig{}, usageErrorf("%s: %v", path, err)
	}
	return cfg, nil
}

func parseConfig(b []byte) (projectConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var cfg projectConfig
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return projectConfig{}, errors.New("empty file; write {} for no settings")
		}
		return projectConfig{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return projectConfig{}, errors.New("unexpected data after the JSON object")
	}
	if err := cfg.checkRules(); err != nil {
		return projectConfig{}, err
	}
	return cfg, nil
}

// checkRules catches redirects and headers missing what every rule needs.
// The server checks the rest (paths, URLs, header names, limits) when the
// deploy is created and names the rule it refuses.
func (cfg projectConfig) checkRules() error {
	if cfg.Redirects != nil {
		for i, r := range *cfg.Redirects {
			switch {
			case r.From == "":
				return fmt.Errorf(`redirects[%d] needs "from", a path such as /old or /blog/*`, i)
			case r.To == "":
				return fmt.Errorf(`redirects[%d] needs "to", a path on the site such as /new or an https:// URL`, i)
			case r.Status != nil && !r.Status.Valid():
				return fmt.Errorf(`redirects[%d] has "status" %d; use 301 (the default), 302, 307 or 308`, i, *r.Status)
			}
		}
	}
	if cfg.Headers != nil {
		for i, h := range *cfg.Headers {
			switch {
			case h.Path == "":
				return fmt.Errorf(`headers[%d] needs "path", such as /* for every path`, i)
			case len(h.Set) == 0:
				return fmt.Errorf(`headers[%d] needs "set", the header names and values to send, such as {"X-Frame-Options": "DENY"}`, i)
			}
		}
	}
	return nil
}

// configServer is the server wardlock.json names, or "".
func (a *app) configServer() (string, error) {
	wd, err := a.getwd()
	if err != nil {
		return "", err
	}
	cfg, err := loadConfig(wd)
	return cfg.Server, err
}
