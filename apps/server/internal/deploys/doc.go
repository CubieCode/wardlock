// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Package deploys owns deploy uploads: manifests and their validation,
// content-addressed blobs (BlobStore), activation, rollback and the Notifier
// that tells the edge a site's served state changed. Blob garbage collection
// (C1.8) and renditions (A2.6) will live here too.
package deploys
