// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import (
	"testing"

	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSingleOwner(t *testing.T) {
	defer test.MockProtect(&SingleOwner)()

	cfg, err := NewConfigProviderFromData("")
	require.NoError(t, err)
	loadSingleOwnerFrom(cfg)
	assert.Empty(t, SingleOwner.PrimaryOwner)

	cfg, err = NewConfigProviderFromData("[single_owner]\nPRIMARY_OWNER = \" alice \"\n")
	require.NoError(t, err)
	loadSingleOwnerFrom(cfg)
	assert.Equal(t, "alice", SingleOwner.PrimaryOwner)
}
