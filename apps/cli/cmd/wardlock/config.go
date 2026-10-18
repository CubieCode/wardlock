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
	return cfg, nil
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
