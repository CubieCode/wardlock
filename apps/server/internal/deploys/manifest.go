// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"wardlock.nz/server/internal/store"
)

// maxPathBytes matches deploy_files.path and the API's ManifestFile.path.
const maxPathBytes = 1024

// maxMessageChars matches deploys.message and the API's Deploy.message.
const maxMessageChars = 500

// Limits bound what one deploy may contain.
type Limits struct {
	MaxFileSize   int64 // bytes per file
	MaxFiles      int   // files per deploy
	MaxDeploySize int64 // total bytes per deploy
}

// DefaultLimits are SPEC's defaults: 25 MiB per file, 20,000 files and
// 1 GiB per deploy.
func DefaultLimits() Limits {
	return Limits{MaxFileSize: 25 << 20, MaxFiles: 20_000, MaxDeploySize: 1 << 30}
}

// Manifest is what a client sends to create a deploy.
type Manifest struct {
	Files   []store.DeployFile
	Message string
	SPA     bool
}

// NormalisePath turns backslashes into forward slashes and checks the
// result against SPEC "Deploys and rollback": valid UTF-8, at most 1024
// bytes, no empty, "." or ".." segments (so no leading or trailing slash),
// no control characters, and not under the reserved .wardlock/ path (in any
// letter case, and including a file named .wardlock itself).
func NormalisePath(p string) (string, error) {
	orig := p
	invalid := func(why string) (string, error) {
		return "", &TargetError{Err: ErrInvalidPath, Target: orig, Message: fmt.Sprintf("Path %q %s.", orig, why)}
	}
	if !utf8.ValidString(p) {
		return invalid("is not valid UTF-8")
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" || len(p) > maxPathBytes {
		return invalid(fmt.Sprintf("must be 1 to %d bytes", maxPathBytes))
	}
	if strings.ContainsFunc(p, unicode.IsControl) {
		return invalid("contains a control character")
	}
	segments := strings.Split(p, "/")
	for _, seg := range segments {
		switch seg {
		case "":
			return invalid("has an empty segment")
		case ".", "..":
			return invalid("contains a . or .. segment")
		}
	}
	if strings.EqualFold(segments[0], ".wardlock") {
		return invalid("is under the reserved .wardlock/ path")
	}
	return p, nil
}

// validate normalises m's paths in place and checks it against the limits.
func (l Limits) validate(m *Manifest) error {
	if len(m.Files) == 0 {
		return &TargetError{Err: ErrInvalidManifest, Target: "files", Message: "A deploy needs at least one file."}
	}
	if utf8.RuneCountInString(m.Message) > maxMessageChars || !utf8.ValidString(m.Message) {
		return &TargetError{Err: ErrInvalidManifest, Target: "message", Message: fmt.Sprintf("The message must be valid UTF-8 of at most %d characters.", maxMessageChars)}
	}
	if len(m.Files) > l.MaxFiles {
		return &TargetError{Err: ErrLimitExceeded, Target: "files", Message: fmt.Sprintf("The deploy has %d files; the limit is %d.", len(m.Files), l.MaxFiles)}
	}
	seen := make(map[string]bool, len(m.Files))
	var total int64
	for i := range m.Files {
		f := &m.Files[i]
		p, err := NormalisePath(f.Path)
		if err != nil {
			return err
		}
		f.Path = p
		if seen[p] {
			return &TargetError{Err: ErrInvalidManifest, Target: p, Message: fmt.Sprintf("Path %q is listed twice.", p)}
		}
		seen[p] = true
		if !ValidHash(f.SHA256) {
			return &TargetError{Err: ErrInvalidManifest, Target: p, Message: fmt.Sprintf("The sha256 of %q must be 64 lowercase hex characters.", p)}
		}
		if f.Size < 0 {
			return &TargetError{Err: ErrInvalidManifest, Target: p, Message: fmt.Sprintf("The size of %q must not be negative.", p)}
		}
		if f.Size > l.MaxFileSize {
			return &TargetError{Err: ErrLimitExceeded, Target: p, Message: fmt.Sprintf("%s is %d bytes; the limit is %d.", p, f.Size, l.MaxFileSize)}
		}
		total += f.Size
		if total > l.MaxDeploySize {
			return &TargetError{Err: ErrLimitExceeded, Target: "files", Message: fmt.Sprintf("The deploy is over %d bytes in total; the limit is %d.", l.MaxDeploySize, l.MaxDeploySize)}
		}
	}
	return nil
}
