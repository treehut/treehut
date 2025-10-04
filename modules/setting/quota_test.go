// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"fmt"
	"testing"

	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigQuotaDefaultTotal(t *testing.T) {
	iniStr := ``
	cfg, err := NewConfigProviderFromData(iniStr)
	require.NoError(t, err)
	loadQuotaFrom(cfg)

	assert.False(t, Quota.Enabled)
	assert.EqualValues(t, -1, Quota.Default.Total)

	testSets := []struct {
		iniTotal    string
		expectTotal int64
	}{
		{"0", 0},
		{"5000", 5000},
		{"12,345,678", 12_345_678},
		{"2k", 2000},
		{"2MiB", 2 * 1024 * 1024},
		{"3G", 3_000_000_000},
		{"3GiB", 3 * 1024 * 1024 * 1024},
		{"9EB", 9_000_000_000_000_000_000},
		{"42EB", -1},
		{"-1", -1},
		{"-42", -1},
		{"-1MiB", -1},
		{"hello", -1},
		{"unlimited", -1},
	}

	for _, testSet := range testSets {
		t.Run(testSet.iniTotal, func(t *testing.T) {
			defer test.MockVariableValue(&Quota.Default.Total, -404)()

			iniStr := fmt.Sprintf(`
[quota]
ENABLED = true
[quota.default]
TOTAL = %s`, testSet.iniTotal)

			cfg, err := NewConfigProviderFromData(iniStr)
			require.NoError(t, err)
			loadQuotaFrom(cfg)

			assert.True(t, Quota.Enabled)
			assert.Equal(t, testSet.expectTotal, Quota.Default.Total)
		})
	}
}
