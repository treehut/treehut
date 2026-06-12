// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package container

import (
	"context"
	"fmt"
	"time"

	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/optional"
	packages_module "forgejo.org/modules/packages"
	packages_service "forgejo.org/services/packages"
	alpine_service "forgejo.org/services/packages/alpine"
	alt_service "forgejo.org/services/packages/alt"
	arch_service "forgejo.org/services/packages/arch"
	cargo_service "forgejo.org/services/packages/cargo"
	container_service "forgejo.org/services/packages/container"
	debian_service "forgejo.org/services/packages/debian"
	rpm_service "forgejo.org/services/packages/rpm"
)

// Task method to execute cleanup rules and cleanup expired package data
func CleanupTask(ctx context.Context, olderThan time.Duration) error {
	if err := ExecuteCleanupRules(ctx); err != nil {
		return err
	}

	return CleanupExpiredData(ctx, olderThan)
}

func ExecuteCleanupRules(ctx context.Context) error {
	return packages_model.IterateEnabledCleanupRules(ctx, func(ctx context.Context, pcr *packages_model.PackageCleanupRule) error {
		// We have no errors to retry on, because we have no evidence we need any in
		// this area. What we do retry on is when a nested `db.RetryTx` indicates to
		// retry the whole transaction.
		return db.RetryTx(ctx, db.RetryConfig{
			AttemptCount: 3,
		}, func(ctx context.Context) error {
			versionsToRemove, err := GetCleanupTargets(ctx, pcr, true)
			if err != nil {
				return fmt.Errorf("CleanupRule [%d]: GetCleanupTargets failed: %w", pcr.ID, err)
			}

			anyVersionDeleted := false
			packageWithVersionDeleted := make(map[int64]bool) // set of Package.ID's where at least one package version was removed
			for _, ct := range versionsToRemove {
				if err := packages_service.DeletePackageVersionAndReferences(ctx, ct.PackageVersion); err != nil {
					return fmt.Errorf("CleanupRule [%d]: DeletePackageVersionAndReferences failed: %w", pcr.ID, err)
				}
				packageWithVersionDeleted[ct.Package.ID] = true
				anyVersionDeleted = true
			}

			if pcr.Type == packages_model.TypeCargo {
				for packageID := range packageWithVersionDeleted {
					owner, err := user_model.GetUserByID(ctx, pcr.OwnerID)
					if err != nil {
						return fmt.Errorf("GetUserByID failed: %w", err)
					}
					if err := cargo_service.UpdatePackageIndexIfExists(ctx, owner, owner, packageID); err != nil {
						return fmt.Errorf("CleanupRule [%d]: cargo.UpdatePackageIndexIfExists failed: %w", pcr.ID, err)
					}
				}
			}

			if anyVersionDeleted {
				switch pcr.Type {
				case packages_model.TypeDebian:
					if err := debian_service.BuildAllRepositoryFiles(ctx, pcr.OwnerID); err != nil {
						return fmt.Errorf("CleanupRule [%d]: debian.BuildAllRepositoryFiles failed: %w", pcr.ID, err)
					}
				case packages_model.TypeAlpine:
					if err := alpine_service.BuildAllRepositoryFiles(ctx, pcr.OwnerID); err != nil {
						return fmt.Errorf("CleanupRule [%d]: alpine.BuildAllRepositoryFiles failed: %w", pcr.ID, err)
					}
				case packages_model.TypeRpm:
					if err := rpm_service.BuildAllRepositoryFiles(ctx, pcr.OwnerID); err != nil {
						return fmt.Errorf("CleanupRule [%d]: rpm.BuildAllRepositoryFiles failed: %w", pcr.ID, err)
					}
				case packages_model.TypeArch:
					if err := arch_service.BuildAllRepositoryFiles(ctx, pcr.OwnerID); err != nil {
						return fmt.Errorf("CleanupRule [%d]: arch.BuildAllRepositoryFiles failed: %w", pcr.ID, err)
					}
				case packages_model.TypeAlt:
					if err := alt_service.BuildAllRepositoryFiles(ctx, pcr.OwnerID); err != nil {
						return fmt.Errorf("CleanupRule [%d]: alt.BuildAllRepositoryFiles failed: %w", pcr.ID, err)
					}
				}
			}
			return nil
		})
	})
}

type CleanupTarget struct {
	Package           *packages_model.Package
	PackageVersion    *packages_model.PackageVersion
	PackageDescriptor *packages_model.PackageDescriptor
}

func GetCleanupTargets(ctx context.Context, pcr *packages_model.PackageCleanupRule, skipPackageDescriptor bool) ([]*CleanupTarget, error) {
	if err := pcr.CompiledPattern(); err != nil {
		return nil, err
	}

	olderThan := time.Now().AddDate(0, 0, -pcr.RemoveDays)

	packages, err := packages_model.GetPackagesByType(ctx, pcr.OwnerID, pcr.Type)
	if err != nil {
		return nil, fmt.Errorf("failure to GetPackagesByType for package cleanup rule: %w", err)
	}

	versionsToRemove := make([]*CleanupTarget, 0, 10)

	for _, p := range packages {
		pvs, _, err := packages_model.SearchVersions(ctx, &packages_model.PackageSearchOptions{
			PackageID:  p.ID,
			IsInternal: optional.Some(false),
			Sort:       packages_model.SortCreatedDesc,
		})
		if err != nil {
			return nil, fmt.Errorf("failure to SearchVersions for package cleanup rule: %w", err)
		}

		var keep int
		for _, pv := range pvs {
			if pcr.Type == packages_model.TypeContainer {
				if skip := container_service.ShouldBeSkipped(pv); skip {
					log.Debug("Rule[%d]: keep '%s/%s' (container)", pcr.ID, p.Name, pv.Version)
					continue
				}
			}

			keep++
			if pcr.KeepCount > 0 && keep <= pcr.KeepCount {
				log.Debug("Rule[%d]: keep '%s/%s' (count)", pcr.ID, p.Name, pv.Version)
				continue
			}

			toMatch := pv.LowerVersion
			if pcr.MatchFullName {
				toMatch = p.LowerName + "/" + pv.LowerVersion
			}

			if pcr.KeepPatternMatcher != nil && pcr.KeepPatternMatcher.MatchString(toMatch) {
				log.Debug("Rule[%d]: keep '%s/%s' (keep pattern)", pcr.ID, p.Name, pv.Version)
				continue
			}
			if pv.CreatedUnix.AsLocalTime().After(olderThan) {
				log.Debug("Rule[%d]: keep '%s/%s' (remove days)", pcr.ID, p.Name, pv.Version)
				continue
			}
			if pcr.RemovePatternMatcher != nil && !pcr.RemovePatternMatcher.MatchString(toMatch) {
				log.Debug("Rule[%d]: keep '%s/%s' (remove pattern)", pcr.ID, p.Name, pv.Version)
				continue
			}

			log.Debug("Rule[%d]: remove '%s/%s'", pcr.ID, p.Name, pv.Version)

			var pd *packages_model.PackageDescriptor
			// GetPackageDescriptor is a bit expensive and can be skipped; only used for cleanup preview to display the package to the UI
			if !skipPackageDescriptor {
				pd, err = packages_model.GetPackageDescriptor(ctx, pv)
				if err != nil {
					return nil, fmt.Errorf("failure to GetPackageDescriptor for package cleanup rule: %w", err)
				}
			}
			versionsToRemove = append(versionsToRemove, &CleanupTarget{
				Package:           p,
				PackageVersion:    pv,
				PackageDescriptor: pd,
			})
		}
	}

	return versionsToRemove, nil
}

func CleanupExpiredData(outerCtx context.Context, olderThan time.Duration) error {
	ctx, committer, err := db.TxContext(outerCtx)
	if err != nil {
		return err
	}
	defer committer.Close()

	if err := container_service.Cleanup(ctx, olderThan); err != nil {
		return err
	}

	pIDs, err := packages_model.FindUnreferencedPackages(ctx)
	if err != nil {
		return err
	}
	for _, pID := range pIDs {
		if err := packages_model.DeleteAllProperties(ctx, packages_model.PropertyTypePackage, pID); err != nil {
			return err
		}
		if err := packages_model.DeletePackageByID(ctx, pID); err != nil {
			return err
		}
	}

	pbs, err := packages_model.FindExpiredUnreferencedBlobs(ctx, olderThan)
	if err != nil {
		return err
	}

	for _, pb := range pbs {
		if err := packages_model.DeleteBlobByID(ctx, pb.ID); err != nil {
			return err
		}
	}

	if err := committer.Commit(); err != nil {
		return err
	}

	contentStore := packages_module.NewContentStore()
	for _, pb := range pbs {
		if err := contentStore.Delete(packages_module.BlobHash256Key(pb.HashSHA256)); err != nil {
			log.Error("Error deleting package blob [%v]: %v", pb.ID, err)
		}
	}

	return nil
}
