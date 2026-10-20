// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package platform

import (
	"fmt"
	"slices"
	"strings"
)

// ModerationOpenAI is the only screening provider (A2.15).
const ModerationOpenAI = "openai"

// ModerationCategories are the OpenAI moderation categories the screening
// variables may name.
var ModerationCategories = []string{
	"harassment", "harassment/threatening", "hate", "hate/threatening", "illicit", "illicit/violent",
	"self-harm", "self-harm/instructions", "self-harm/intent", "sexual", "sexual/minors", "violence", "violence/graphic",
}

// ModerationConfig is content screening (A2.15; docs/DEPLOYMENT.md
// "Content screening"). Provider is "" (off, the default) or
// ModerationOpenAI, which needs OpenAIKey.
type ModerationConfig struct {
	Provider  string
	OpenAIKey Secret
	// Quarantine lists the categories that withhold a file; Flag those that
	// mark it for review, nil meaning every flagged category not in
	// Quarantine.
	Quarantine []string
	Flag       []string
}

// loadModeration reads WARDLOCK_MODERATION, WARDLOCK_OPENAI_API_KEY,
// WARDLOCK_MODERATION_QUARANTINE and WARDLOCK_MODERATION_FLAG.
func (c *Config) loadModeration(get func(key, fallback string) string) error {
	m := ModerationConfig{OpenAIKey: Secret(get("WARDLOCK_OPENAI_API_KEY", ""))}
	switch p := strings.ToLower(get("WARDLOCK_MODERATION", "off")); p {
	case "off":
	case ModerationOpenAI:
		if m.OpenAIKey == "" {
			return fmt.Errorf("%w: WARDLOCK_MODERATION=openai needs WARDLOCK_OPENAI_API_KEY", ErrInvalidConfig)
		}
		m.Provider = p
	default:
		return fmt.Errorf("%w: WARDLOCK_MODERATION must be off or openai", ErrInvalidConfig)
	}
	var err error
	if m.Quarantine, err = categoryList("WARDLOCK_MODERATION_QUARANTINE", get("WARDLOCK_MODERATION_QUARANTINE", "sexual/minors,sexual")); err != nil {
		return err
	}
	if raw := get("WARDLOCK_MODERATION_FLAG", ""); raw != "" {
		if m.Flag, err = categoryList("WARDLOCK_MODERATION_FLAG", raw); err != nil {
			return err
		}
	}
	c.Moderation = m
	return nil
}

// categoryList reads a comma-separated list of moderation categories;
// "none" is the empty list.
func categoryList(key, raw string) ([]string, error) {
	out := []string{}
	if strings.EqualFold(strings.TrimSpace(raw), "none") {
		return out, nil
	}
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if !slices.Contains(ModerationCategories, item) {
			return nil, fmt.Errorf("%w: %s: %q is not an OpenAI moderation category (%s)", ErrInvalidConfig, key, item, strings.Join(ModerationCategories, ", "))
		}
		if !slices.Contains(out, item) {
			out = append(out, item)
		}
	}
	return out, nil
}
