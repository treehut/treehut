// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	actions_model "forgejo.org/models/actions"
)

const (
	preStepName  = "Set up job"
	postStepName = "Complete job"
)

// FullSteps returns steps with "Set up job" and "Complete job"
func FullSteps(task *actions_model.ActionTask) []*actions_model.ActionTaskStep {
	if len(task.Steps) == 0 {
		return fullStepsOfEmptySteps(task)
	}

	// firstStep is the first step that has run or running, not include preStep.
	// For example,
	// 1. preStep(Success) -> step1(Success) -> step2(Running) -> step3(Waiting) -> postStep(Waiting): firstStep is step1.
	// 2. preStep(Success) -> step1(Skipped) -> step2(Success) -> postStep(Success): firstStep is step2.
	// 3. preStep(Success) -> step1(Running) -> step2(Waiting) -> postStep(Waiting): firstStep is step1.
	// 4. preStep(Success) -> step1(Skipped) -> step2(Skipped) -> postStep(Skipped): firstStep is nil.
	// 5. preStep(Success) -> step1(Cancelled) -> step2(Cancelled) -> postStep(Cancelled): firstStep is nil.
	var firstStep *actions_model.ActionTaskStep
	// lastHasRunStep is the last step that has run.
	// For example,
	// 1. preStep(Success) -> step1(Success) -> step2(Running) -> step3(Waiting) -> postStep(Waiting): lastHasRunStep is step1.
	// 2. preStep(Success) -> step1(Success) -> step2(Success) -> step3(Success) -> postStep(Success): lastHasRunStep is step3.
	// 3. preStep(Success) -> step1(Success) -> step2(Failure) -> step3 -> postStep(Waiting): lastHasRunStep is step2.
	// So its Stopped is the Started of postStep when there are no more steps to run.
	var lastHasRunStep *actions_model.ActionTaskStep
	var lastDoneStep *actions_model.ActionTaskStep

	var logIndex int64
	for _, step := range task.Steps {
		if firstStep == nil && (step.Status.HasRun() || step.Status.IsRunning()) {
			firstStep = step
		}
		if step.Status.HasRun() {
			lastHasRunStep = step
		}
		if step.Status.IsDone() { // may be a skipped step, which doesn't have a Stopped timestamp
			lastDoneStep = step
		}
		logIndex += step.LogLength
	}

	preStep := &actions_model.ActionTaskStep{
		Name:      preStepName,
		LogLength: task.LogLength,
		Started:   task.Started,
		Status:    actions_model.StatusRunning,
	}

	// No step has run or is running, so preStep is equal to the task
	if firstStep == nil {
		preStep.Stopped = task.Stopped
		preStep.Status = task.Status
	} else {
		preStep.LogLength = firstStep.LogIndex
		preStep.Stopped = firstStep.Started
		preStep.Status = actions_model.StatusSuccess
	}
	logIndex += preStep.LogLength

	if lastHasRunStep == nil {
		lastHasRunStep = preStep
	}
	if lastDoneStep == nil {
		lastDoneStep = preStep
	}

	postStep := &actions_model.ActionTaskStep{
		Name:   postStepName,
		Status: actions_model.StatusWaiting,
	}
	// If the lastHasRunStep is the last step, or it has failed, postStep has started.
	if lastHasRunStep.Status.IsFailure() || lastDoneStep == task.Steps[len(task.Steps)-1] {
		postStep.LogIndex = logIndex
		postStep.LogLength = task.LogLength - postStep.LogIndex
		postStep.Started = lastHasRunStep.Stopped
		postStep.Status = actions_model.StatusRunning
	}
	if task.Status.IsDone() {
		postStep.Status = task.Status
		postStep.Stopped = task.Stopped
	}
	ret := make([]*actions_model.ActionTaskStep, 0, len(task.Steps)+2)
	ret = append(ret, preStep)
	ret = append(ret, task.Steps...)
	ret = append(ret, postStep)

	return ret
}

// StepByteRange returns the [start, end) byte window in task's log file
// covered by step, using task.LogIndexes for line->byte translation and
// falling back to task.LogSize for the tail. Returns (0, 0) when the step
// has no logs yet (LogLength <= 0 or LogIndex beyond the recorded indexes).
func StepByteRange(task *actions_model.ActionTask, step *actions_model.ActionTaskStep) (start, end int64) {
	if step.LogLength <= 0 || step.LogIndex >= int64(len(task.LogIndexes)) {
		return 0, 0
	}
	start = task.LogIndexes[step.LogIndex]
	endIdx := step.LogIndex + step.LogLength
	if endIdx < int64(len(task.LogIndexes)) {
		end = task.LogIndexes[endIdx]
	} else {
		// Step's log range extends beyond recorded indexes (step is still
		// running or task hasn't fully landed yet).
		end = task.LogSize
	}
	return start, end
}

func fullStepsOfEmptySteps(task *actions_model.ActionTask) []*actions_model.ActionTaskStep {
	preStep := &actions_model.ActionTaskStep{
		Name:      preStepName,
		LogLength: task.LogLength,
		Started:   task.Started,
		Stopped:   task.Stopped,
		Status:    actions_model.StatusRunning,
	}

	postStep := &actions_model.ActionTaskStep{
		Name:     postStepName,
		LogIndex: task.LogLength,
		Started:  task.Stopped,
		Stopped:  task.Stopped,
		Status:   actions_model.StatusWaiting,
	}

	if task.Status.IsDone() {
		preStep.Status = task.Status
		if preStep.Status.IsSuccess() {
			postStep.Status = actions_model.StatusSuccess
		} else if preStep.Status.IsSkipped() {
			postStep.Status = actions_model.StatusSkipped
		} else {
			postStep.Status = actions_model.StatusCancelled
		}
	}

	return []*actions_model.ActionTaskStep{
		preStep,
		postStep,
	}
}
