// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package activities

import "fmt"

type ErrActivityPrivate struct {
	id int64
}

func (err ErrActivityPrivate) Error() string {
	return fmt.Sprintf("Activity with id %d is private", err.id)
}
