// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package deploys owns deploy uploads: manifests and their validation,
// content-addressed blobs (BlobStore), activation, rollback and the Notifier
// that tells the edge a site's served state changed, and garbage collection
// (GC), and the AI-readable renditions (A2.6) generated from each deploy's
// HTML files before activation.
package deploys
