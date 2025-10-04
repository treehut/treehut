// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/validation"
	"forgejo.org/tests"

	"github.com/gobwas/glob"
	"github.com/stretchr/testify/assert"
)

func TestEmailBlocklist(t *testing.T) {
	defer test.MockVariableValue(
		&setting.Service.EmailDomainBlockList,
		[]glob.Glob{glob.MustCompile("evil")},
	)()

	defer tests.PrepareTestEnv(t)()

	emailValid, ok := validation.IsEmailDomainAllowed("🐸@pond")
	assert.True(t, emailValid)
	assert.True(t, ok)

	emailValid, ok = validation.IsEmailDomainAllowed("🐸@pond (what-is-this@evil)")
	assert.True(t, emailValid)
	assert.True(t, ok)

	emailValid, ok = validation.IsEmailDomainAllowed("jomo@evil")
	assert.True(t, emailValid)
	assert.False(t, ok)

	emailValid, ok = validation.IsEmailDomainAllowed("jomo@evil (but-does-it@break)")
	assert.True(t, emailValid)
	assert.False(t, ok)
}

func TestEmailAllowlist(t *testing.T) {
	defer test.MockVariableValue(
		&setting.Service.EmailDomainAllowList,
		[]glob.Glob{glob.MustCompile("pond")},
	)()

	defer tests.PrepareTestEnv(t)()

	emailValid, ok := validation.IsEmailDomainAllowed("🐸@pond")
	assert.True(t, emailValid)
	assert.True(t, ok)

	emailValid, ok = validation.IsEmailDomainAllowed("🐸@pond (what-is-this@evil)")
	assert.True(t, emailValid)
	assert.True(t, ok)

	emailValid, ok = validation.IsEmailDomainAllowed("jomo@evil")
	assert.True(t, emailValid)
	assert.False(t, ok)

	emailValid, ok = validation.IsEmailDomainAllowed("jomo@evil (but-does-it@break)")
	assert.True(t, emailValid)
	assert.False(t, ok)
}
