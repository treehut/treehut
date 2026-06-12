// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"strings"
	"time"

	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/optional"
	"forgejo.org/modules/timeutil"

	"github.com/gdgvda/cron"
)

// ActionScheduleSpec represents a schedule spec of a workflow file
type ActionScheduleSpec struct {
	ID         int64
	RepoID     int64                  `xorm:"index"`
	Repo       *repo_model.Repository `xorm:"-"`
	ScheduleID int64                  `xorm:"index"`
	Schedule   *ActionSchedule        `xorm:"-"`

	// Next time the job will run, or the zero time if Cron has not been
	// started or this entry's schedule is unsatisfiable
	Next timeutil.TimeStamp `xorm:"index"`
	// Prev is the last time this job was run, or the zero time if never.
	Prev     timeutil.TimeStamp
	Spec     string
	TimeZone optional.Option[string]

	Created timeutil.TimeStamp `xorm:"created"`
	Updated timeutil.TimeStamp `xorm:"updated"`
}

func NewActionScheduleSpec(cron string, tz optional.Option[string], referenceTime time.Time) (*ActionScheduleSpec, error) {
	spec := &ActionScheduleSpec{
		Spec:     cron,
		TimeZone: tz,
	}
	cronSchedule, err := spec.Parse()
	if err != nil {
		return nil, err
	}

	spec.Next = timeutil.TimeStamp(cronSchedule.Next(referenceTime).Unix())
	return spec, nil
}

// Parse parses the spec and returns a cron.Schedule
// Unlike the default cron parser, Parse uses UTC timezone as the default if none is specified.
func (s *ActionScheduleSpec) Parse() (cron.Schedule, error) {
	parser, err := cron.NewDefaultParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if err != nil {
		return nil, err
	}

	schedule, err := parser.Parse(s.Spec)
	if err != nil {
		return nil, err
	}

	// If `timezone` is not defined in the workflow, but the spec includes a timezone, use it.
	if !s.TimeZone.Has() && (strings.HasPrefix(s.Spec, "TZ=") || strings.HasPrefix(s.Spec, "CRON_TZ=")) {
		return schedule, nil
	}

	var location *time.Location
	if present, tz := s.TimeZone.Get(); present {
		location, err = time.LoadLocation(tz)
		if err != nil {
			return nil, err
		}
	} else {
		// UTC is the default time zone.
		location = time.UTC
	}

	return schedule.WithLocation(location), nil
}

func init() {
	db.RegisterModel(new(ActionScheduleSpec))
}

func UpdateScheduleSpec(ctx context.Context, spec *ActionScheduleSpec, cols ...string) error {
	sess := db.GetEngine(ctx).ID(spec.ID)
	if len(cols) > 0 {
		sess.Cols(cols...)
	}
	_, err := sess.Update(spec)
	return err
}
