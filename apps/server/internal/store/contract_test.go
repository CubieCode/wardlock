// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store_test

import (
	"testing"

	"wardlock.nz/server/internal/store/storetest"
)

func TestRepositoryContracts(t *testing.T) {
	storetest.RunContract(t, storetest.SQLite)
}
