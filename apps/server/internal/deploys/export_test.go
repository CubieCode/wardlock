// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package deploys

import "os"

// SetSyncForTest replaces the fsync the store uses for files and
// directories, so tests can observe it.
func (s *FSBlobStore) SetSyncForTest(fn func(*os.File) error) { s.sync = fn }
