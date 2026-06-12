// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package secret

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"forgejo.org/models/db"
	"forgejo.org/modules/keying"
	"forgejo.org/modules/log"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"

	"xorm.io/builder"
)

var (
	namePattern            = regexp.MustCompile("(?i)^[A-Z_][A-Z0-9_]*$")
	forbiddenPrefixPattern = regexp.MustCompile("(?i)^(FORGEJO_|GITEA_|GITHUB_|[0-9])")

	ErrInvalidName = util.NewInvalidArgumentErrorf("invalid secret name")
)

// Secret represents a secret
//
// It can be:
//  1. org/user level secret, OwnerID is org/user ID and RepoID is 0
//  2. repo level secret, OwnerID is 0 and RepoID is repo ID
//
// Please note that it's not acceptable to have both OwnerID and RepoID to be non-zero,
// or it will be complicated to find secrets belonging to a specific owner.
// For example, conditions like `OwnerID = 1` will also return secret {OwnerID: 1, RepoID: 1},
// but it's a repo level secret, not an org/user level secret.
// To avoid this, make it clear with {OwnerID: 0, RepoID: 1} for repo level secrets.
//
// Please note that it's not acceptable to have both OwnerID and RepoID to zero, global secrets are not supported.
// It's for security reasons, admin may be not aware of that the secrets could be stolen by any user when setting them as global.
type Secret struct {
	ID          int64
	OwnerID     int64              `xorm:"INDEX UNIQUE(owner_repo_name) NOT NULL"`
	RepoID      int64              `xorm:"INDEX UNIQUE(owner_repo_name) NOT NULL DEFAULT 0"`
	Name        string             `xorm:"UNIQUE(owner_repo_name) NOT NULL"`
	Data        []byte             `xorm:"BLOB"` // encrypted data
	CreatedUnix timeutil.TimeStamp `xorm:"created NOT NULL"`
}

// ErrSecretNotFound represents a "secret not found" error.
type ErrSecretNotFound struct {
	Name string
}

func (err ErrSecretNotFound) Error() string {
	return fmt.Sprintf("secret was not found [name: %s]", err.Name)
}

func (err ErrSecretNotFound) Unwrap() error {
	return util.ErrNotExist
}

// InsertEncryptedSecret Creates, encrypts, and validates a new secret with yet unencrypted data and insert into database
func InsertEncryptedSecret(ctx context.Context, ownerID, repoID int64, name, data string) (*Secret, error) {
	if ownerID != 0 && repoID != 0 {
		// It's trying to create a secret that belongs to a repository, but OwnerID has been set accidentally.
		// Remove OwnerID to avoid confusion; it's not worth returning an error here.
		ownerID = 0
	}
	if ownerID == 0 && repoID == 0 {
		return nil, fmt.Errorf("%w: ownerID and repoID cannot be both zero, global secrets are not supported", util.ErrInvalidArgument)
	}
	if err := ValidateName(name); err != nil {
		return nil, err
	}

	secret := &Secret{
		OwnerID: ownerID,
		RepoID:  repoID,
		Name:    strings.ToUpper(name),
	}

	return secret, db.WithTx(ctx, func(ctx context.Context) error {
		if err := db.Insert(ctx, secret); err != nil {
			return err
		}

		secret.SetData(data)
		_, err := db.GetEngine(ctx).ID(secret.ID).Cols("data").Update(secret)
		return err
	})
}

func init() {
	db.RegisterModel(new(Secret))
}

type FindSecretsOptions struct {
	db.ListOptions
	RepoID   int64
	OwnerID  int64 // it will be ignored if RepoID is set
	SecretID int64
	Name     string
}

func (opts FindSecretsOptions) ToConds() builder.Cond {
	cond := builder.NewCond()

	cond = cond.And(builder.Eq{"repo_id": opts.RepoID})
	if opts.RepoID != 0 { // if RepoID is set
		// ignore OwnerID and treat it as 0
		cond = cond.And(builder.Eq{"owner_id": 0})
	} else {
		cond = cond.And(builder.Eq{"owner_id": opts.OwnerID})
	}

	if opts.SecretID != 0 {
		cond = cond.And(builder.Eq{"id": opts.SecretID})
	}
	if opts.Name != "" {
		cond = cond.And(builder.Eq{"name": strings.ToUpper(opts.Name)})
	}

	return cond
}

func (s *Secret) SetData(data string) {
	normalizedData := util.ReserveLineBreakForTextarea(data)
	s.Data = keying.ActionSecret.Encrypt([]byte(normalizedData), keying.ColumnAndID("data", s.ID))
}

func (s *Secret) GetDecryptedData() (string, error) {
	key := keying.ActionSecret
	v, err := key.Decrypt(s.Data, keying.ColumnAndID("data", s.ID))
	if err != nil {
		return "", fmt.Errorf("unable to decrypt secret[id=%d,name=%q]: %w", s.ID, s.Name, err)
	}

	return string(v), nil
}

func GetSecretByID(ctx context.Context, ownerID, repoID, id int64) (*Secret, error) {
	query := db.GetEngine(ctx).Where("id=?", id)

	if repoID > 0 {
		query = query.And(builder.Eq{"repo_id": repoID})
	} else if ownerID > 0 {
		query = query.And(builder.Eq{"owner_id": ownerID})
	} else {
		return nil, fmt.Errorf("ownerID and repoID cannot be simultaneously 0")
	}

	var secret Secret
	has, err := query.Get(&secret)

	if err != nil {
		return nil, err
	} else if !has {
		return nil, fmt.Errorf("secret with ID %d: %w", id, util.ErrNotExist)
	}
	return &secret, nil
}

func UpdateSecret(ctx context.Context, secret *Secret, columns ...string) error {
	e := db.GetEngine(ctx)

	if err := ValidateName(secret.Name); err != nil {
		return err
	}
	secret.Name = strings.ToUpper(secret.Name)

	var err error
	if len(columns) == 0 {
		_, err = e.ID(secret.ID).AllCols().Update(secret)
	} else {
		_, err = e.ID(secret.ID).Cols(columns...).Update(secret)
	}

	return err
}

func FetchActionSecrets(ctx context.Context, ownerID, repoID int64) (map[string]string, error) {
	secrets := map[string]string{}

	ownerSecrets, err := db.Find[Secret](ctx, FindSecretsOptions{OwnerID: ownerID})
	if err != nil {
		log.Error("find secrets of owner %v: %v", ownerID, err)
		return nil, err
	}
	repoSecrets, err := db.Find[Secret](ctx, FindSecretsOptions{RepoID: repoID})
	if err != nil {
		log.Error("find secrets of repo %v: %v", repoID, err)
		return nil, err
	}

	for _, secret := range append(ownerSecrets, repoSecrets...) {
		decryptedData, err := secret.GetDecryptedData()
		if err != nil {
			log.Error("%v", err)
			return nil, err
		}
		secrets[secret.Name] = decryptedData
	}

	return secrets, nil
}

func ValidateName(name string) error {
	if !namePattern.MatchString(name) || forbiddenPrefixPattern.MatchString(name) {
		return ErrInvalidName
	}
	return nil
}
