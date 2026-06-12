<script>
import {SvgIcon} from '../svg.js';
import ActionRunStatus from './ActionRunStatus.vue';
import ActionJobStepList from './ActionJobStepList.vue';
import {toggleElem} from '../utils/dom.js';
import {GET, POST, DELETE} from '../modules/fetch.js';

export default {
  name: 'RepoActionView',
  components: {
    SvgIcon,
    ActionRunStatus,
    ActionJobStepList,
  },
  props: {
    initialJobData: {
      type: Object,
      required: true,
    },
    initialArtifactData: {
      type: Object,
      required: true,
    },
    runIndex: {
      type: String,
      required: true,
    },
    runID: {
      type: String,
      required: true,
    },
    jobIndex: {
      type: String,
      required: true,
    },
    attemptNumber: {
      type: String,
      required: true,
    },
    actionsURL: {
      type: String,
      required: true,
    },
    workflowName: {
      type: String,
      required: true,
    },
    workflowURL: {
      type: String,
      required: true,
    },
    workflowSourceURL: {
      type: String,
      required: true,
    },
    locale: {
      type: Object,
      required: true,
    },
  },

  data() {
    return {
      // internal state
      loading: false,
      initialLoadComplete: false,
      needLoadingWithLogCursors: null,
      intervalID: null,
      lineNumberOffset: [],
      currentJobStepsStates: [],
      artifacts: [],
      menuVisible: undefined,
      isFullScreen: false,
      timeVisible: {
        'log-time-stamp': false,
        'log-time-seconds': false,
      },

      // provided by backend
      run: {
        link: '',
        title: '',
        titleHTML: '',
        status: '',
        description: '',
        canCancel: false,
        canApprove: false,
        canRerun: false,
        done: false,
        preExecutionError: '',
        jobs: [
          // {
          //   id: 0,
          //   name: '',
          //   status: '',
          //   canRerun: false,
          //   duration: '',
          // },
        ],
        commit: {
          localeWorkflow: '',
          localeAllRuns: '',
          shortSHA: '',
          link: '',
          pusher: {
            displayName: '',
            link: '',
          },
          branch: {
            name: '',
            link: '',
          },
        },
      },
      currentJob: {
        title: '',
        details: [],
        steps: [
          // {
          //   summary: '',
          //   duration: '',
          //   status: '',
          // }
        ],
        // All available attempts for the job we're currently viewing.
        //
        // initial value here is configured so that currentlyViewingMostRecentAttempt() -> true on the default `data()`, so that the
        // initial render (before `loadJob`'s first execution is complete) doesn't display "You are viewing an
        // out-of-date run..."
        allAttempts: [],
      },
    };
  },

  computed: {
    shouldShowAttemptDropdown() {
      return this.initialLoadComplete && this.currentJob.allAttempts && this.currentJob.allAttempts.length > 1;
    },

    displayOtherJobs() {
      return this.currentlyViewingMostRecentAttempt;
    },

    canApprove() {
      return this.currentlyViewingMostRecentAttempt && this.run.canApprove;
    },

    canCancel() {
      return this.currentlyViewingMostRecentAttempt && this.run.canCancel;
    },

    canRerun() {
      return this.currentlyViewingMostRecentAttempt && this.run.canRerun;
    },

    viewingAttemptNumber() {
      return parseInt(this.attemptNumber);
    },

    viewingAttempt() {
      const fallback = {index: 0, time_since_started_html: '', status: 'success'};
      if (!this.currentJob.allAttempts) {
        return fallback;
      }

      const attempt = this.currentJob.allAttempts.find((attempt) => attempt.number === this.viewingAttemptNumber);
      return attempt || fallback;
    },

    currentlyViewingMostRecentAttempt() {
      if (!this.currentJob.allAttempts || this.currentJob.allAttempts.length === 0) {
        return true;
      }

      const mostRecentAttemptNumber = this.currentJob.allAttempts[0].number;
      return this.viewingAttemptNumber === mostRecentAttemptNumber;
    },

    displayGearDropdown() {
      return this.menuVisible === 'gear';
    },

    displayAttemptDropdown() {
      return this.menuVisible === 'attempt';
    },

    viewingOutOfDateRunLabel() {
      return this.locale.viewingOutOfDateRun
        .replace('%[1]s', this.viewingAttempt.time_since_started_html);
    },

    statusDiagnostics() {
      if (!this.currentJob.allAttempts) {
        return this.currentJob.details;
      }

      const useAttempt = this.currentJob.allAttempts.some((attempt) => attempt.number === this.viewingAttemptNumber);
      if (useAttempt) {
        return this.viewingAttempt.status_diagnostics;
      }
      return this.currentJob.details;
    },
  },

  async mounted() {
    // Need to await first loadJob so this.currentJobStepsStates is initialized and can be used in hashChangeListener,
    // but with the initializing data being passed in this should end up as a synchronous invocation.  loadJob is
    // responsible for setting up its refresh interval during this first invocation.
    await this.loadJob({initialJobData: this.initialJobData, initialArtifactData: this.initialArtifactData});
    document.body.addEventListener('click', this.closeDropdown);
    this.hashChangeListener();
    window.addEventListener('hashchange', this.hashChangeListener);
  },

  beforeUnmount() {
    document.body.removeEventListener('click', this.closeDropdown);
    window.removeEventListener('hashchange', this.hashChangeListener);
  },

  unmounted() {
    // clear the interval timer when the component is unmounted
    // even our page is rendered once, not spa style
    if (this.intervalID) {
      clearInterval(this.intervalID);
      this.intervalID = null;
    }
  },

  methods: {
    // show/hide the step logs for a step
    toggleStepLogs(idx) {
      this.currentJobStepsStates[idx].expanded = !this.currentJobStepsStates[idx].expanded;
      if (this.currentJobStepsStates[idx].expanded) {
        // request data load immediately instead of waiting for next timer interval (which, if the job is done, will
        // never happen because the interval will have been disabled)
        this.loadJob();
      }
    },

    // cancel a run
    cancelRun() {
      POST(`${this.run.link}/cancel`);
    },

    // approve a run
    approveRun() {
      const url = `${this.run.commit.branch.link}#pull-request-trust-panel`;
      window.location.href = url;
    },

    appendLogs(stepIndex, logLines, startTime) {
      this.$refs.stepList.appendLogs(stepIndex, logLines, startTime);
    },

    async fetchArtifacts() {
      const resp = await GET(`${this.actionsURL}/runs/${this.runIndex}/artifacts`);
      return await resp.json();
    },

    async deleteArtifact(name) {
      if (!window.confirm(this.locale.confirmDeleteArtifact.replace('%s', name))) return;
      await DELETE(`${this.run.link}/artifacts/${name}`);
      await this.loadJob();
    },

    getLogCursors() {
      return this.currentJobStepsStates.map((it, idx) => {
        // cursor is used to indicate the last position of the logs
        // it's only used by backend, frontend just reads it and passes it back, it and can be any type.
        // for example: make cursor=null means the first time to fetch logs, cursor=eof means no more logs, etc
        return {step: idx, cursor: it.cursor, expanded: it.expanded};
      });
    },

    async fetchJob(logCursors) {
      const resp = await POST(
        `${this.actionsURL}/runs/${this.runIndex}/jobs/${this.jobIndex}/attempt/${this.attemptNumber}`,
        {data: {logCursors}},
      );
      return await resp.json();
    },

    async loadJob(initializationData) {
      const isInitializing = initializationData !== undefined;
      let myLoadingLogCursors = this.getLogCursors();
      if (this.loading) {
        // loadJob is already executing; but it's possible that our log cursor request has changed since it started.  If
        // the interval load is active, that problem would solve itself, but if it isn't (say we're viewing a "done"
        // job), then the change to the requested cursors may never be loaded.  To address this we set our newest
        // requested log cursors into a state property and rely on loadJob to retry at the end of its execution if it
        // notices these have changed.
        this.needLoadingWithLogCursors = myLoadingLogCursors;
        return;
      }

      try {
        this.loading = true;
        // Since no async operations occurred since fetching myLoadingLogCursors, we can be sure that we have the most
        // recent needed log cursors, so we can reset needLoadingWithLogCursors -- it could be stale if exceptions
        // occurred in previous load attempts.
        this.needLoadingWithLogCursors = null;

        let job, artifacts;

        while (true) {
          try {
            if (initializationData) {
              job = initializationData.initialJobData;
              artifacts = initializationData.initialArtifactData;
              // don't think it's possible that we loop retrying for 'needLoadingWithLogCursors' during initialization,
              // but just in case, we'll ensure initializationData can only be used once and go to the network on retry
              initializationData = undefined;
            } else {
              [job, artifacts] = await Promise.all([
                this.fetchJob(myLoadingLogCursors),
                this.fetchArtifacts(), // refresh artifacts if upload-artifact step done
              ]);
            }
          } catch (err) {
            if (err instanceof TypeError) return; // avoid network error while unloading page
            throw err;
          }

          // We can be done as long as needLoadingWithLogCursors is null, or the same as what we just loaded.
          if (this.needLoadingWithLogCursors === null || JSON.stringify(this.needLoadingWithLogCursors) === JSON.stringify(myLoadingLogCursors)) {
            this.needLoadingWithLogCursors = null;
            break;
          }

          // Otherwise we need to retry that.
          myLoadingLogCursors = this.needLoadingWithLogCursors;
        }

        this.artifacts = artifacts['artifacts'] || [];

        // save the state to Vue data, then the UI will be updated
        this.run = job.state.run;
        this.currentJob = job.state.currentJob;

        // sync the currentJobStepsStates to store the job step states
        for (let i = 0; i < this.currentJob.steps.length; i++) {
          if (!this.currentJobStepsStates[i]) {
            // initial states for job steps
            this.currentJobStepsStates[i] = {cursor: null, expanded: false};
          }
        }
        // append logs to the UI
        for (const logs of job.logs.stepsLog) {
          // save the cursor, it will be passed to backend next time
          this.lineNumberOffset[logs.step] = 0;
          this.currentJobStepsStates[logs.step].cursor = logs.cursor;
          this.appendLogs(logs.step, logs.lines, logs.started);
        }

        if (this.run.done) {
          if (this.intervalID) {
            clearInterval(this.intervalID);
            this.intervalID = null;
          }
        } else if (isInitializing) {
          // Begin refresh interval since we know this job isn't done.
          this.intervalID = setInterval(this.loadJob, 1000);
        }
      } finally {
        this.loading = false;
        this.initialLoadComplete = true;
      }
    },

    navigateToAttempt(attempt) {
      const url = `${this.actionsURL}/runs/${this.runIndex}/jobs/${this.jobIndex}/attempt/${attempt.number}`;
      window.location.href = url;
    },

    navigateToMostRecentAttempt() {
      const url = `${this.actionsURL}/runs/${this.runIndex}/jobs/${this.jobIndex}`;
      window.location.href = url;
    },

    isDone(status) {
      return ['success', 'skipped', 'failure', 'cancelled'].includes(status);
    },

    isExpandable(status) {
      return ['success', 'running', 'failure', 'cancelled'].includes(status);
    },

    toggleAttemptDropdown() {
      if (this.menuVisible === 'attempt') {
        this.menuVisible = undefined;
      } else {
        this.menuVisible = 'attempt';
      }
    },

    toggleGearDropdown() {
      if (this.menuVisible === 'gear') {
        this.menuVisible = undefined;
      } else {
        this.menuVisible = 'gear';
      }
    },

    closeDropdown() {
      this.menuVisible = undefined;
    },

    toggleTimeDisplay(type) {
      this.timeVisible[`log-time-${type}`] = !this.timeVisible[`log-time-${type}`];
    },

    toggleFullScreen() {
      this.isFullScreen = !this.isFullScreen;
      const fullScreenEl = document.querySelector('.action-view-right');
      const outerEl = document.querySelector('.full.height');
      const actionBodyEl = document.querySelector('.action-view-body');
      const headerEl = document.querySelector('#navbar');
      const contentEl = document.querySelector('.page-content.repository');
      const footerEl = document.querySelector('.page-footer');
      toggleElem(headerEl, !this.isFullScreen);
      toggleElem(contentEl, !this.isFullScreen);
      toggleElem(footerEl, !this.isFullScreen);
      // move .action-view-right to new parent
      if (this.isFullScreen) {
        outerEl.append(fullScreenEl);
      } else {
        actionBodyEl.append(fullScreenEl);
      }
    },
    async hashChangeListener() {
      const selectedLogStep = window.location.hash;
      if (!selectedLogStep) return;
      const [_, step, _line] = selectedLogStep.split('-');
      if (!this.currentJobStepsStates[step]) return;
      if (!this.currentJobStepsStates[step].expanded && this.currentJobStepsStates[step].cursor === null) {
        this.currentJobStepsStates[step].expanded = true;
        // need to await for load job if the step log is loaded for the first time
        // so logline can be selected by querySelector
        await this.loadJob();
      }
      this.$refs.stepList.scrollIntoView(step, selectedLogStep);
    },

    runAttemptLabel(attempt) {
      if (!attempt) {
        return '';
      }
      return this.locale.runAttemptLabel
        .replace('%[1]s', attempt.number)
        .replace('%[2]s', attempt.time_since_started_html);
    },
  },
};
</script>
<template>
  <div class="ui container fluid padded action-view-container" :class="{ 'interval-pending': intervalID }">
    <div class="action-view-header job-out-of-date-warning" v-if="!currentlyViewingMostRecentAttempt">
      <div class="ui warning message">
        <!-- eslint-disable-next-line vue/no-v-html -->
        <span v-html="viewingOutOfDateRunLabel"/>
        <button class="tw-ml-8 ui basic small compact button" @click="navigateToMostRecentAttempt()">
          {{ locale.viewMostRecentRun }}
        </button>
      </div>
    </div>
    <div class="action-view-header">
      <div class="action-info-summary">
        <div class="action-info-summary-title">
          <ActionRunStatus :locale-status="locale.status[run.status]" :status="run.status" :size="20"/>
          <!-- eslint-disable-next-line vue/no-v-html -->
          <h2 class="action-info-summary-title-text" v-html="run.titleHTML"/>
        </div>
        <button class="ui basic small compact button primary" @click="approveRun()" v-if="canApprove">
          {{ locale.approve }}
        </button>
        <div class="action-info-summary-actions" v-else>
          <button class="ui basic small compact button red" @click="cancelRun()" v-if="canCancel">
            {{ locale.cancel }}
          </button>
          <button class="ui basic small compact button tw-mr-0 tw-whitespace-nowrap link-action" :data-url="`${run.link}/rerun`" v-if="canRerun">
            {{ locale.rerun_all }}
          </button>
        </div>
      </div>
      <div class="action-summary">
        <!-- eslint-disable-next-line vue/no-v-html -->
        <span v-html="run.description"/>
        <span class="ui label tw-max-w-full" v-if="run.commit.shortSHA">
          <span v-if="run.commit.branch.isDeleted" class="gt-ellipsis tw-line-through" :data-tooltip-content="run.commit.branch.name">{{ run.commit.branch.name }}</span>
          <a v-else class="gt-ellipsis" :href="run.commit.branch.link" :data-tooltip-content="run.commit.branch.name">{{ run.commit.branch.name }}</a>
        </span>
      </div>
      <div class="action-summary">
        {{ run.commit.localeWorkflow }}
        <a :href="workflowSourceURL">{{ workflowName }}</a> <span>(<a :href="workflowURL">{{ run.commit.localeAllRuns }}</a>)</span>
      </div>
      <div class="ui error message pre-execution-error" v-if="run.preExecutionError">
        <div class="header">
          {{ locale.preExecutionError }}
        </div>
        {{ run.preExecutionError }}
      </div>
    </div>
    <div class="action-view-body">
      <div class="action-view-left" v-if="displayOtherJobs">
        <div class="job-group-section">
          <div class="job-brief-list">
            <a class="job-brief-item" :href="run.link+'/jobs/'+index" :class="parseInt(jobIndex) === index ? 'selected' : ''" v-for="(job, index) in run.jobs" :key="job.id">
              <div class="job-brief-item-left">
                <ActionRunStatus :locale-status="locale.status[job.status]" :status="job.status"/>
                <span class="job-brief-name tw-mx-2 gt-ellipsis">{{ job.name }}</span>
              </div>
              <span class="job-brief-item-right">
                <SvgIcon name="octicon-sync" role="button" :data-tooltip-content="locale.rerun" class="job-brief-rerun tw-mx-3 link-action" :data-url="`${run.link}/jobs/${index}/rerun`" v-if="job.canRerun"/>
                <span class="step-summary-duration">{{ job.duration }}</span>
              </span>
            </a>
          </div>
        </div>
        <div class="job-artifacts" v-if="artifacts.length > 0">
          <div class="job-artifacts-title">
            {{ locale.artifactsTitle }}
          </div>
          <ul class="job-artifacts-list">
            <li class="job-artifacts-item" v-for="artifact in artifacts" :key="artifact.name">
              <div v-if="artifact.status === 'expired'">
                <SvgIcon name="octicon-file" class="ui text black job-artifacts-icon"/>{{ artifact.name }}
              </div>
              <template v-if="artifact.status !== 'expired'">
                <a class="job-artifacts-link" target="_blank" :href="actionsURL+'/runs/'+runID+'/artifacts/'+artifact.name">
                  <SvgIcon name="octicon-file" class="ui text black job-artifacts-icon"/>{{ artifact.name }}
                </a>
                <a v-if="run.canDeleteArtifact" @click="deleteArtifact(artifact.name)" class="job-artifacts-delete">
                  <SvgIcon name="octicon-trash" class="ui text black job-artifacts-icon"/>
                </a>
              </template>
            </li>
          </ul>
        </div>
      </div>

      <div class="action-view-right">
        <div class="job-info-header">
          <div class="job-info-header-left gt-ellipsis">
            <h3 class="job-info-header-title gt-ellipsis">
              {{ currentJob.title }}
            </h3>
            <ul class="job-info-header-detail">
              <li v-for="detail in statusDiagnostics" :key="detail">
                {{ detail }}
              </li>
            </ul>
          </div>
          <div class="job-info-header-right job-attempt-dropdown tw-mr-8" v-if="shouldShowAttemptDropdown" v-cloak>
            <div class="ui dropdown selection" @click.stop="toggleAttemptDropdown()">
              <SvgIcon name="octicon-triangle-down" class="dropdown icon"/>
              <div class="default text">
                <ActionRunStatus :locale-status="locale.status[viewingAttempt.status]" :status="viewingAttempt.status" :inline="true"/>
                <!-- eslint-disable-next-line vue/no-v-html -->
                <span class="tw-ml-2" v-html="runAttemptLabel(viewingAttempt)"/>
              </div>
              <div class="menu transition action-job-menu" :class="{visible: displayAttemptDropdown}" v-if="displayAttemptDropdown" v-cloak>
                <a tabindex="0" :class="{ item: true, selected: attempt.number === viewingAttemptNumber }" v-for="attempt in currentJob.allAttempts" :key="attempt.number" @click="navigateToAttempt(attempt)">
                  <ActionRunStatus :locale-status="locale.status[attempt.status]" :status="attempt.status" :inline="true"/>
                  <!-- eslint-disable-next-line vue/no-v-html -->
                  <span class="tw-ml-2" v-html="runAttemptLabel(attempt)"/>
                </a>
              </div>
            </div>
          </div>
          <div class="job-info-header-right">
            <div class="ui top right pointing dropdown dark-dropdown custom jump item job-gear-dropdown" @click.stop="toggleGearDropdown()">
              <button class="btn interact-bg tw-p-2">
                <SvgIcon name="octicon-gear" :size="18"/>
              </button>
              <div class="menu transition action-job-menu" :class="{visible: displayGearDropdown}" v-if="displayGearDropdown" v-cloak>
                <a class="item" tabindex="0" @click="toggleTimeDisplay('seconds')" @keyup.space="toggleTimeDisplay('seconds')" @keyup.enter="toggleTimeDisplay('seconds')">
                  <i class="icon"><SvgIcon :name="timeVisible['log-time-seconds'] ? 'octicon-check' : 'gitea-empty-checkbox'"/></i>
                  {{ locale.showLogSeconds }}
                </a>
                <a class="item" tabindex="0" @click="toggleTimeDisplay('stamp')" @keyup.space="toggleTimeDisplay('stamp')" @keyup.enter="toggleTimeDisplay('stamp')">
                  <i class="icon"><SvgIcon :name="timeVisible['log-time-stamp'] ? 'octicon-check' : 'gitea-empty-checkbox'"/></i>
                  {{ locale.showTimeStamps }}
                </a>
                <a class="item" tabindex="0" @click="toggleFullScreen()" @keyup.space="toggleFullScreen()" @keyup.enter="toggleFullScreen()">
                  <i class="icon"><SvgIcon :name="isFullScreen ? 'octicon-check' : 'gitea-empty-checkbox'"/></i>
                  {{ locale.showFullScreen }}
                </a>
                <div class="divider"/>
                <a :class="['item', !currentJob.steps.length ? 'disabled' : '']" :href="run.link+'/jobs/'+jobIndex+'/attempt/'+viewingAttemptNumber+'/logs'" target="_blank">
                  <i class="icon"><SvgIcon name="octicon-download"/></i>
                  {{ locale.downloadLogs }}
                </a>
              </div>
            </div>
          </div>
        </div>
        <ActionJobStepList
          ref="stepList"
          :steps="currentJob.steps"
          :step-states="currentJobStepsStates"
          :run-status="run.status"
          :is-expandable="isExpandable"
          :is-done="isDone"
          :time-visible-timestamp="timeVisible['log-time-stamp']"
          :time-visible-seconds="timeVisible['log-time-seconds']"
          @toggle-step-logs="toggleStepLogs"
        />
      </div>
    </div>
  </div>
</template>
<style scoped>
.action-view-body {
  padding-top: 12px;
  padding-bottom: 12px;
  display: flex;
  gap: 12px;
}

/* ================ */
/* action view header */

.action-view-header {
  margin-top: 8px;
}

.action-info-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.action-info-summary-title {
  display: flex;
  align-items: center;
  gap: 0.5em;
}

.action-info-summary-actions {
  display: flex;
  align-items: center;
  gap: var(--button-spacing);
  margin-left: auto;
}

.action-info-summary-actions > button {
  margin: 0;
}

.action-info-summary-title-text {
  font-size: 20px;
  margin: 0;
  flex: 1;
  overflow-wrap: anywhere;
}

.action-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
  margin-left: 28px;
}

@media (max-width: 767.98px) {
  .action-commit-summary {
    margin-left: 0;
    margin-top: 8px;
  }
}

/* ================ */
/* action view left */

.action-view-left {
  width: 30%;
  max-width: 400px;
  position: sticky;
  top: 12px;
  max-height: 100vh;
  overflow-y: auto;
  background: var(--color-body);
  z-index: 2; /* above .job-info-header */
}

@media (max-width: 767.98px) {
  .action-view-left {
    position: static; /* can not sticky because multiple jobs would overlap into right view */
  }
}

.job-artifacts-title {
  font-size: 18px;
  margin-top: 16px;
  padding: 16px 10px 0 20px;
  border-top: 1px solid var(--color-secondary);
}

.job-artifacts-item {
  margin: 5px 0;
  padding: 6px;
  display: flex;
  justify-content: space-between;
}

.job-artifacts-list {
  padding-left: 12px;
  list-style: none;
}

.job-artifacts-icon {
  padding-right: 3px;
}

.job-brief-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.job-brief-item {
  padding: 10px;
  border-radius: var(--border-radius);
  text-decoration: none;
  display: flex;
  flex-wrap: nowrap;
  justify-content: space-between;
  align-items: center;
  color: var(--color-text);
}

.job-brief-item:hover {
  background-color: var(--color-hover);
}

.job-brief-item.selected {
  font-weight: var(--font-weight-bold);
  background-color: var(--color-active);
}

.job-brief-item:first-of-type {
  margin-top: 0;
}

.job-brief-item .job-brief-rerun {
  cursor: pointer;
  transition: transform 0.2s;
}

.job-brief-item .job-brief-rerun:hover {
  transform: scale(130%);
}

.job-brief-item .job-brief-item-left {
  display: flex;
  width: 100%;
  min-width: 0;
}

.job-brief-item .job-brief-item-left span {
  display: flex;
  align-items: center;
}

.job-brief-item .job-brief-item-left .job-brief-name {
  display: block;
  width: 70%;
}

.job-brief-item .job-brief-item-right {
  display: flex;
  align-items: center;
}

/* ================ */
/* action view right */

.action-view-right {
  flex: 1;
  color: var(--color-console-fg-subtle);
  max-height: 100%;
  width: 70%;
  display: flex;
  flex-direction: column;
  border: 1px solid var(--color-console-border);
  border-radius: var(--border-radius);
  background: var(--color-console-bg);
  align-self: flex-start;
}

/* begin fomantic button overrides */

.action-view-right .ui.button,
.action-view-right .ui.button:focus {
  background: transparent;
  color: var(--color-console-fg-subtle);
}

.action-view-right .ui.button:hover {
  background: var(--color-console-hover-bg);
  color: var(--color-console-fg);
}

.action-view-right .ui.button:active {
  background: var(--color-console-active-bg);
  color: var(--color-console-fg);
}

/* end fomantic button overrides */

/* begin fomantic dropdown menu overrides */

.action-view-right .ui.dropdown.dark-dropdown .menu {
  background: var(--color-console-menu-bg);
  border-color: var(--color-console-menu-border);
}

.action-view-right .ui.dropdown.dark-dropdown .menu > .item {
  color: var(--color-console-fg);
}

.action-view-right .ui.dropdown.dark-dropdown .menu > .item:hover {
  color: var(--color-console-fg);
  background: var(--color-console-hover-bg);
}

.action-view-right .ui.dropdown.dark-dropdown .menu > .item:active {
  color: var(--color-console-fg);
  background: var(--color-console-active-bg);
}

.action-view-right .ui.dropdown.dark-dropdown .menu > .divider {
  border-top-color: var(--color-console-menu-border);
}

.action-view-right .ui.pointing.dropdown.dark-dropdown > .menu:not(.hidden)::after {
  background: var(--color-console-menu-bg);
  box-shadow: -1px -1px 0 0 var(--color-console-menu-border);
}

/* end fomantic dropdown menu overrides */

.job-info-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 12px;
  position: sticky;
  top: 0;
  height: 60px;
  z-index: 1; /* above .job-step-container */
  background: var(--color-console-bg);
  border-radius: 3px;
}

.job-info-header:has(+ .job-step-container) {
  border-radius: var(--border-radius) var(--border-radius) 0 0;
}

.job-info-header .job-info-header-title {
  color: var(--color-console-fg);
  font-size: 16px;
  margin: 0;
}

.job-info-header .job-info-header-detail {
  color: var(--color-console-fg-subtle);
  font-size: 12px;
  list-style: none;
  padding: 0;
  margin: 0;
}

.job-info-header-left {
  flex: 1;
}

@media (max-width: 767.98px) {
  .action-view-body {
    flex-direction: column;
  }
  .action-view-left, .action-view-right {
    width: 100%;
  }
  .action-view-left {
    max-width: none;
  }
}
</style>

<style>
/* some elements are not managed by vue, so we need to use global style */
/* selectors here are intentionally exact to only match fullscreen */

.full.height > .action-view-right {
  width: 100%;
  height: 100%;
  padding: 0;
  border-radius: 0;
}

.full.height > .action-view-right > .job-info-header {
  border-radius: 0;
}

.full.height > .action-view-right > .job-step-container {
  height: calc(100% - 60px);
  border-radius: 0;
}

.job-log-list.hidden {
  display: none;
}
</style>
