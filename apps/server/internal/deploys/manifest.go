// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import (
	"fmt"
	"maps"
	"path"
	"slices"
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
	// MaxVideoSize is the per-file limit for video files (IsVideo), on top
	// of MaxFileSize; 0 refuses video files altogether (A2.14).
	MaxVideoSize int64
	// BlockedExtensions are lowercase extensions with their dot, such as
	// ".exe", that no deploy may contain (A2.14); nil blocks none.
	BlockedExtensions map[string]bool
}

// DefaultLimits are SPEC's defaults: 25 MiB per file, 10 MiB per video
// file, 20,000 files and 1 GiB per deploy, and no blocked extensions.
func DefaultLimits() Limits {
	return Limits{MaxFileSize: 25 << 20, MaxFiles: 20_000, MaxDeploySize: 1 << 30, MaxVideoSize: 10 << 20}
}

// videoExtensions are the extensions IsVideo recognises. Detection is by
// extension alone, so every server agrees: the host's MIME table is not
// consulted (it varies between machines, and some map .ts, a TypeScript
// file far more often than an MPEG transport stream, to video/mp2t).
var videoExtensions = map[string]bool{
	".3g2": true, ".3gp": true, ".asf": true, ".avi": true, ".f4v": true,
	".flv": true, ".m2ts": true, ".m2v": true, ".m4v": true, ".mkv": true,
	".mov": true, ".mp4": true, ".mpeg": true, ".mpg": true, ".mts": true,
	".ogv": true, ".qt": true, ".vob": true, ".webm": true, ".wmv": true,
}

// IsVideo reports whether the path's extension, in any letter case, is a
// video format's.
func IsVideo(p string) bool { return videoExtensions[extension(p)] }

// VideoExtensions returns the extensions IsVideo recognises, sorted.
func VideoExtensions() []string { return slices.Sorted(maps.Keys(videoExtensions)) }

// extension returns p's extension in lowercase with its dot, or "".
func extension(p string) string { return strings.ToLower(path.Ext(p)) }

// Manifest is what a client sends to create a deploy.
type Manifest struct {
	Files   []store.DeployFile
	Message string
	SPA     bool
	// ImmutableAssets opts hashed files under /assets/ into long-term
	// caching while the deploy is active.
	ImmutableAssets bool
}

// NormalisePath turns backslashes into forward slashes and checks the
// result against SPEC "Deploys and rollback": valid UTF-8, at most 1024
// bytes, no empty, "." or ".." segments (so no leading or trailing slash),
// no control (Cc) or format (Cf: zero-width, bidi overrides, BOM)
// characters, and not under the reserved .wardlock/ path (in any
// letter case, and including a file named .wardlock itself). Paths are
// otherwise kept byte for byte, with no Unicode normalisation; the CLI
// (C1.5) sends them in NFC.
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
	if strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return invalid("contains a control or invisible format character")
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
	sizes := make(map[string]int64, len(m.Files)) // one size per hash
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
		if size, ok := sizes[f.SHA256]; ok && size != f.Size {
			return &TargetError{Err: ErrInvalidManifest, Target: p, Message: fmt.Sprintf("%q gives size %d for a hash listed elsewhere with size %d.", p, f.Size, size)}
		}
		sizes[f.SHA256] = f.Size
		if ext := extension(p); l.BlockedExtensions[ext] {
			return &TargetError{Err: ErrBlockedType, Target: p, Message: fmt.Sprintf("%s is a %s file, a type this server does not accept.", p, ext)}
		}
		if IsVideo(p) {
			if l.MaxVideoSize == 0 {
				return &TargetError{Err: ErrLimitExceeded, Target: p, Message: fmt.Sprintf("%s is a video file; this server does not accept video files.", p)}
			}
			if f.Size > l.MaxVideoSize {
				return &TargetError{Err: ErrLimitExceeded, Target: p, Message: fmt.Sprintf("%s is a video file of %s; the limit for video files is %s.", p, FormatBytes(f.Size), FormatBytes(l.MaxVideoSize))}
			}
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

// FormatBytes writes n in the largest binary unit that holds it exactly,
// such as "10 MiB", or else as bytes with a rounded figure alongside, such
// as "15728641 bytes (15.0 MiB)", so a size just over a limit never reads
// the same as the limit.
func FormatBytes(n int64) string {
	units := []string{"bytes", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v := n; i < len(units)-1 && v >= 1024 && v%1024 == 0; v /= 1024 {
		i++
	}
	if i > 0 {
		return fmt.Sprintf("%d %s", n>>(10*i), units[i])
	}
	if n == 1 {
		return "1 byte"
	}
	if n < 1024 {
		return fmt.Sprintf("%d bytes", n)
	}
	f, i := float64(n), 0
	for ; f >= 1024 && i < len(units)-1; f /= 1024 {
		i++
	}
	return fmt.Sprintf("%d bytes (%.1f %s)", n, f, units[i])
}
