// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"strings"
	"testing"
	"time"
)

func TestCertsReady(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	tests := []struct {
		name     string
		expiries map[string]time.Time
		want     []string // substrings of the error; nil means ready
	}{
		{"all valid", map[string]time.Time{"*.sites.test": now.Add(time.Hour), "dash.test": now.Add(time.Hour)}, nil},
		{"none yet", map[string]time.Time{"*.sites.test": {}, "dash.test": now.Add(time.Hour)}, []string{"no certificate yet for *.sites.test"}},
		{"expired", map[string]time.Time{"*.sites.test": now.Add(-time.Second)}, []string{"certificate for *.sites.test expired"}},
		{"both", map[string]time.Time{"a.test": {}, "b.test": now}, []string{"a.test", "b.test expired"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := certsReady(tt.expiries, now)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("certsReady = %v, want ready", err)
				}
				return
			}
			if err == nil {
				t.Fatal("certsReady passed")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("certsReady = %q, want it to mention %q", err, w)
				}
			}
		})
	}
}
