// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package method

import (
	"errors"
	"fmt"
	"net/http"

	"forgejo.org/services/auth"
)

// Ensure the struct implements the interface.
var (
	_ auth.Method = &Group{}
)

// Group implements the Auth interface with serval Auth.
type Group struct {
	methods []auth.Method
}

// NewGroup creates a new auth group
func NewGroup(methods ...auth.Method) *Group {
	return &Group{
		methods: methods,
	}
}

// Add adds a new method to group
func (b *Group) Add(method auth.Method) {
	b.methods = append(b.methods, method)
}

func (b *Group) Verify(req *http.Request, w http.ResponseWriter, sess auth.SessionStore) auth.MethodOutput {
	var incorrectCredentials []error

	for _, m := range b.methods {
		output := m.Verify(req, w, sess)

		switch v := output.(type) {
		case *auth.AuthenticationSuccess, *auth.AuthenticationError:
			return v

		case *auth.AuthenticationNotAttempted:
			// Move on to the next supported authentication method.
			continue

		case *auth.AuthenticationAttemptedIncorrectCredential:
			// Move on to the next supported authentication method, but keep a record of this error.  If none of the
			// other methods are able to authenticate the user, we'll report this as an incorrect credential (401) case.
			incorrectCredentials = append(incorrectCredentials, v.Error)
			continue

		default:
			return &auth.AuthenticationError{Error: fmt.Errorf("unexpected result from Method.Verify on method %v: %v", m, v)}
		}
	}

	if len(incorrectCredentials) != 0 {
		return &auth.AuthenticationAttemptedIncorrectCredential{Error: errors.Join(incorrectCredentials...)}
	}

	return &auth.AuthenticationNotAttempted{}
}
