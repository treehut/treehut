// Copyright 2026 The Forgejo Authors.
// SPDX-License-Identifier: GPL-3.0-or-later
package packages

import (
	"testing"

	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCalculateFileSize(t *testing.T) {
	defer unittest.OverrideFixtures("models/packages/fixtures/TestCalculateFileSize")()
	require.NoError(t, unittest.PrepareTestDatabase())

	// blob1 size is 10
	// blob2 size is 20
	// blob3 size is 15
	size, err := CalculateFileSize(t.Context(), &PackageFileSearchOptions{
		OwnerID: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(30), size) // 2*blob1 + blob2 = 30

	size, err = CalculateFileSize(t.Context(), &PackageFileSearchOptions{
		OwnerID: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(30), size) // 2*blob2 + blob1 = 30

	size, err = CalculateFileSize(t.Context(), &PackageFileSearchOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(45), size) // 2*blob1 + 2*blob2 + blob3 = 45
}
