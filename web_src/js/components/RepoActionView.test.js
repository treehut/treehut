import {mount, flushPromises} from '@vue/test-utils';
import {toAbsoluteUrl} from '../utils.js';
import RepoActionView from './RepoActionView.vue';

const testLocale = {
  approve: 'Locale Approve',
  cancel: 'Locale Cancel',
  rerun: 'Locale Re-run',
  artifactsTitle: 'artifactTitleHere',
  areYouSure: '',
  confirmDeleteArtifact: '',
  rerun_all: '',
  showTimeStamps: '',
  showLogSeconds: '',
  showFullScreen: '',
  downloadLogs: '',
  runAttemptLabel: 'Run attempt %[1]s %[2]s',
  viewingOutOfDateRun: 'oh no, out of date since %[1]s give or take or so',
  viewMostRecentRun: '',
  preExecutionError: 'pre-execution error',
  status: {
    unknown: '',
    waiting: '',
    running: '',
    success: '',
    failure: '',
    cancelled: '',
    skipped: '',
    blocked: '',
  },
};
const minimalInitialJobData = {
  state: {
    run: {
      status: 'success',
      commit: {
        pusher: {},
      },
    },
    currentJob: {
      steps: [
        {
          summary: 'Test Job',
          duration: '1s',
          status: 'success',
        },
      ],
    },
  },
  logs: {
    stepsLog: [],
  },
};
const minimalInitialArtifactData = {
  artifacts: [],
};
const defaultTestProps = {
  actionsURL: 'https://example.com/example-org/example-repo/actions',
  jobIndex: '1',
  attemptNumber: '1',
  runIndex: '10',
  runID: '1001',
  initialJobData: minimalInitialJobData,
  initialArtifactData: minimalInitialArtifactData,
  locale: testLocale,
  workflowName: 'workflow name',
  workflowURL: 'https://example.com/example-org/example-repo/actions?workflow=test.yml',
  workflowSourceURL: 'https://example.com/example-org/example-repo/src/commit/023babec384/.forgejo/workflows/test.yml',
};

test('load multiple steps on a finished action', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  vi.spyOn(global, 'fetch').mockImplementation((url, opts) => {
    if (url.endsWith('/artifacts')) {
      return Promise.resolve({
        ok: true,
        json: vi.fn().mockResolvedValue(
          {
            artifacts: [],
          },
        ),
      });
    }

    const postBody = JSON.parse(opts.body);
    const stepsLog_value = [];
    for (const cursor of postBody.logCursors) {
      if (cursor.expanded) {
        stepsLog_value.push(
          {
            step: cursor.step,
            cursor: 0,
            lines: [
              {index: 1, message: `Step #${cursor.step + 1} Log #1`, timestamp: 0},
              {index: 1, message: `Step #${cursor.step + 1} Log #2`, timestamp: 0},
              {index: 1, message: `Step #${cursor.step + 1} Log #3`, timestamp: 0},
            ],
          },
        );
      }
    }
    const jobs_value = {
      state: {
        run: {
          status: 'success',
          commit: {
            pusher: {},
          },
        },
        currentJob: {
          title: 'test',
          steps: [
            {
              summary: 'Test Step #1',
              duration: '1s',
              status: 'success',
            },
            {
              summary: 'Test Step #2',
              duration: '1s',
              status: 'success',
            },
          ],
          allAttempts: [{number: 1, time_since_started_html: '', status: 'success', status_diagnostics: ['Success']}],
        },
      },
      logs: {
        stepsLog: opts.body?.includes('"cursor":null') ? stepsLog_value : [],
      },
    };

    return Promise.resolve({
      ok: true,
      json: vi.fn().mockResolvedValue(
        jobs_value,
      ),
    });
  });

  const wrapper = mount(RepoActionView, {
    props: defaultTestProps,
  });
  wrapper.vm.loadJob(); // simulate intermittent reload immediately so UI switches from minimalInitialJobData to the mock data from the test's fetch spy.
  await flushPromises();
  // Click on both steps to start their log loading in fast succession...
  await wrapper.get('.job-step-section:nth-of-type(1) .job-step-summary').trigger('click');
  await wrapper.get('.job-step-section:nth-of-type(2) .job-step-summary').trigger('click');
  await flushPromises();

  // Verify both step's logs were loaded
  expect(wrapper.get('.job-step-section:nth-of-type(1) .job-log-line:nth-of-type(1) .log-msg').text()).toEqual('Step #1 Log #1');
  expect(wrapper.get('.job-step-section:nth-of-type(1) .job-log-line:nth-of-type(2) .log-msg').text()).toEqual('Step #1 Log #2');
  expect(wrapper.get('.job-step-section:nth-of-type(1) .job-log-line:nth-of-type(3) .log-msg').text()).toEqual('Step #1 Log #3');
  expect(wrapper.get('.job-step-section:nth-of-type(2) .job-log-line:nth-of-type(1) .log-msg').text()).toEqual('Step #2 Log #1');
  expect(wrapper.get('.job-step-section:nth-of-type(2) .job-log-line:nth-of-type(2) .log-msg').text()).toEqual('Step #2 Log #2');
  expect(wrapper.get('.job-step-section:nth-of-type(2) .job-log-line:nth-of-type(3) .log-msg').text()).toEqual('Step #2 Log #3');

  // Attempt status
  expect(wrapper.get('.job-info-header h3').text()).toEqual('test');
  expect(wrapper.findAll('ul.job-info-header-detail li').length).toEqual(1);
  expect(wrapper.get('ul.job-info-header-detail li:nth-child(1)').text()).toEqual('Success');
});

function configureForMultipleAttemptTests({viewHistorical}) {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  const myJobState = {
    run: {
      canApprove: true,
      canCancel: true,
      canRerun: true,
      status: 'success',
      commit: {
        pusher: {},
      },
    },
    currentJob: {
      title: 'test',
      steps: [
        {
          summary: 'Test Job',
          duration: '1s',
          status: 'success',
        },
      ],
      allAttempts: [
        {number: 3, time_since_started_html: 'yesterday', status: 'success', status_diagnostics: ['Success']},
        // Omit one attempt to simulate the case when a job isn't run because a `needs:` failed.
        {number: 1, time_since_started_html: 'two days ago', status: 'failure', status_diagnostics: ['Failure']},
      ],
    },
  };
  vi.spyOn(global, 'fetch').mockImplementation((url, opts) => {
    const artifacts_value = {
      artifacts: [],
    };
    const stepsLog_value = [
      {
        step: 0,
        cursor: 0,
        lines: [],
      },
    ];
    const jobs_value = {
      state: myJobState,
      logs: {
        stepsLog: opts.body?.includes('"cursor":null') ? stepsLog_value : [],
      },
    };

    return Promise.resolve({
      ok: true,
      json: vi.fn().mockResolvedValue(
        url.endsWith('/artifacts') ? artifacts_value : jobs_value,
      ),
    });
  });

  const wrapper = mount(RepoActionView, {
    props: {
      ...defaultTestProps,
      runIndex: '123',
      attemptNumber: viewHistorical ? '1' : '3',
      actionsURL: toAbsoluteUrl('/user1/repo2/actions'),
      initialJobData: {...minimalInitialJobData, state: myJobState},
    },
  });
  return wrapper;
}

test('display baseline with most-recent attempt', async () => {
  const wrapper = configureForMultipleAttemptTests({viewHistorical: false});
  await flushPromises();

  // Warning dialog for viewing an out-of-date attempt...
  expect(wrapper.findAll('.job-out-of-date-warning').length).toEqual(0);

  // Approve button should be visible; can't have all three at once but at least this verifies the inverse of the
  // historical attempt test below.
  expect(wrapper.findAll('button').filter((button) => button.text() === 'Locale Approve').length).toEqual(1);

  // Job list will be visible...
  expect(wrapper.findAll('.job-group-section').length).toEqual(1);

  // Attempt selector dropdown...
  expect(wrapper.findAll('.job-attempt-dropdown').length).toEqual(1);
  expect(wrapper.findAll('.job-attempt-dropdown .svg.octicon-check-circle-fill.text.green').length).toEqual(1);
  expect(wrapper.get('.job-attempt-dropdown .ui.dropdown').text()).toEqual('Run attempt 3 yesterday');

  // Attempt status
  expect(wrapper.get('.job-info-header h3').text()).toEqual('test');
  expect(wrapper.findAll('ul.job-info-header-detail li').length).toEqual(1);
  expect(wrapper.get('ul.job-info-header-detail li:nth-child(1)').text()).toEqual('Success');
});

test('display reconfigured for historical attempt', async () => {
  const wrapper = configureForMultipleAttemptTests({viewHistorical: true});
  await flushPromises();

  // Warning dialog for viewing an out-of-date attempt...
  expect(wrapper.findAll('.job-out-of-date-warning').length).toEqual(1);
  expect(wrapper.get('.job-out-of-date-warning').text()).toEqual('oh no, out of date since two days ago give or take or so');
  await wrapper.get('.job-out-of-date-warning button').trigger('click');
  expect(window.location.href).toEqual(toAbsoluteUrl('/user1/repo2/actions/runs/123/jobs/1'));
  // eslint-disable-next-line no-restricted-globals
  history.back();
  await flushPromises();

  // Approve, Cancel, Re-run all buttons should all be suppressed...
  expect(wrapper.findAll('button').filter((button) => button.text() === 'Locale Approve').length).toEqual(0);
  expect(wrapper.findAll('button').filter((button) => button.text() === 'Locale Cancel').length).toEqual(0);
  expect(wrapper.findAll('button').filter((button) => button.text() === 'Locale Re-run').length).toEqual(0);

  // Job list will be suppressed...
  expect(wrapper.findAll('.job-group-section').length).toEqual(0);

  // Attempt selector dropdown...
  expect(wrapper.findAll('.job-attempt-dropdown').length).toEqual(1);
  expect(wrapper.findAll('.job-attempt-dropdown .svg.octicon-x-circle-fill.text.red').length).toEqual(1);
  expect(wrapper.get('.job-attempt-dropdown .ui.dropdown').text()).toEqual('Run attempt 1 two days ago');

  // Attempt status
  expect(wrapper.get('.job-info-header h3').text()).toEqual('test');
  expect(wrapper.findAll('ul.job-info-header-detail li').length).toEqual(1);
  expect(wrapper.get('ul.job-info-header-detail li:nth-child(1)').text()).toEqual('Failure');
});

test('historical attempt dropdown interactions', async () => {
  const wrapper = configureForMultipleAttemptTests({viewHistorical: true});
  await flushPromises();

  // Check dropdown exists, but isn't expanded.
  const attemptsNotExpanded = () => {
    expect(wrapper.findAll('.job-attempt-dropdown').length).toEqual(1);
    expect(wrapper.findAll('.job-attempt-dropdown .action-job-menu').length).toEqual(0, 'dropdown content not yet visible');
  };
  attemptsNotExpanded();

  // Click on attempt dropdown
  wrapper.get('.job-attempt-dropdown .ui.dropdown').trigger('click');
  await flushPromises();

  // Check dropdown is expanded and both options are displayed
  const attemptsExpanded = () => {
    expect(wrapper.findAll('.job-attempt-dropdown .action-job-menu').length).toEqual(1);
    expect(wrapper.get('.job-attempt-dropdown .action-job-menu').isVisible()).toBe(true);
    expect(wrapper.findAll('.job-attempt-dropdown .action-job-menu a').filter((a) => a.text() === 'Run attempt 3 yesterday').length).toEqual(1);
    expect(wrapper.findAll('.job-attempt-dropdown .action-job-menu a').filter((a) => a.text() === 'Run attempt 1 two days ago').length).toEqual(1);
  };
  attemptsExpanded();

  // Normally dismiss occurs on a body click event; simulate that by calling `closeDropdown()`
  wrapper.vm.closeDropdown();
  await flushPromises();

  // Should return to not expanded.
  attemptsNotExpanded();

  // Click on the gear dropdown
  wrapper.get('.job-gear-dropdown').trigger('click');
  await flushPromises();

  // Check that gear's menu is expanded, and attempt dropdown isn't.
  expect(wrapper.findAll('.job-gear-dropdown .action-job-menu').length).toEqual(1);
  expect(wrapper.get('.job-gear-dropdown .action-job-menu').isVisible()).toBe(true);
  attemptsNotExpanded();

  // Click on attempt dropdown
  wrapper.get('.job-attempt-dropdown .ui.dropdown').trigger('click');
  await flushPromises();

  // Check that attempt dropdown expanded again, gear dropdown disappeared (mutually exclusive)
  expect(wrapper.findAll('.job-gear-dropdown .action-job-menu').length).toEqual(0);
  attemptsExpanded();

  // Click on the other option in the dropdown to verify it navigates to the target attempt
  wrapper.findAll('.job-attempt-dropdown .action-job-menu a').find((a) => a.text() === 'Run attempt 3 yesterday').trigger('click');
  expect(window.location.href).toEqual(toAbsoluteUrl('/user1/repo2/actions/runs/123/jobs/1/attempt/3'));
});

test('run approval interaction', async () => {
  const pullRequestLink = '/example-org/example-repo/pulls/456';
  const wrapper = mount(RepoActionView, {
    props: {
      ...defaultTestProps,
      initialJobData: {
        state: {
          run: {
            canApprove: true,
            status: 'waiting',
            commit: {
              pusher: {},
              branch: {
                link: toAbsoluteUrl(pullRequestLink),
              },
            },
          },
          currentJob: {
            steps: [
              {
                summary: 'Test Job',
                duration: '1s',
                status: 'success',
              },
            ],
          },
        },
        logs: {
          stepsLog: [],
        },
      },
    },
  });
  await flushPromises();
  const approve = wrapper.findAll('button').filter((button) => button.text() === 'Locale Approve');
  expect(approve.length).toEqual(1);
  approve[0].trigger('click');
  expect(window.location.href).toEqual(toAbsoluteUrl(`${pullRequestLink}#pull-request-trust-panel`));
});

test('artifacts download links', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  vi.spyOn(global, 'fetch').mockImplementation((url, opts) => {
    if (url.endsWith('/artifacts')) {
      return Promise.resolve({
        ok: true,
        json: vi.fn().mockResolvedValue(
          {
            artifacts: [
              {name: 'artifactname1', size: 111, status: 'completed'},
              {name: 'artifactname2', size: 222, status: 'expired'},
            ],
          },
        ),
      });
    }

    const postBody = JSON.parse(opts.body);
    const stepsLog_value = [];
    for (const cursor of postBody.logCursors) {
      if (cursor.expanded) {
        stepsLog_value.push(
          {
            step: cursor.step,
            cursor: 0,
            lines: [
              {index: 1, message: `Step #${cursor.step + 1} Log #1`, timestamp: 0},
            ],
          },
        );
      }
    }
    const jobs_value = {
      state: {
        run: {
          status: 'success',
          commit: {
            pusher: {},
          },
        },
        currentJob: {
          title: 'test',
          steps: [
            {
              summary: 'Test Step #1',
              duration: '1s',
              status: 'success',
            },
          ],
          allAttempts: [{number: 1, time_since_started_html: '', status: 'success', status_diagnostics: ['Success']}],
        },
      },
      logs: {
        stepsLog: opts.body?.includes('"cursor":null') ? stepsLog_value : [],
      },
    };

    return Promise.resolve({
      ok: true,
      json: vi.fn().mockResolvedValue(
        jobs_value,
      ),
    });
  });

  const wrapper = mount(RepoActionView, {
    props: defaultTestProps,
  });
  wrapper.vm.loadJob(); // simulate intermittent reload immediately so UI switches from minimalInitialJobData to the mock data from the test's fetch spy.
  await flushPromises();

  expect(wrapper.get('.job-artifacts .job-artifacts-title').text()).toEqual('artifactTitleHere');

  expect(wrapper.find('.job-artifacts .job-artifacts-item:nth-of-type(1) a').exists()).toBe(true);
  // Expired artifacts should be listed, but not be linked, because they no longer exist.
  expect(wrapper.find('.job-artifacts .job-artifacts-item:nth-of-type(2) a').exists()).toBe(false);

  expect(wrapper.get('.job-artifacts .job-artifacts-item:nth-of-type(1) .job-artifacts-link').attributes('href')).toEqual('https://example.com/example-org/example-repo/actions/runs/1001/artifacts/artifactname1');
});

test('initial load schedules refresh when job is not done', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  vi.spyOn(global, 'fetch').mockImplementation((url, _opts) => {
    return Promise.resolve({
      ok: true,
      json: vi.fn().mockResolvedValue(
        url.endsWith('/artifacts') ? minimalInitialArtifactData : minimalInitialJobData,
      ),
    });
  });

  // Provide a job that is "done" so that the component doesn't start incremental refresh...
  {
    const doneInitialJobData = structuredClone(minimalInitialJobData);
    doneInitialJobData.state.run.done = true;
    const wrapper = mount(RepoActionView, {
      props: {
        ...defaultTestProps,
        initialJobData: doneInitialJobData,
      },
    });
    await flushPromises();
    const container = wrapper.find('.action-view-container');
    expect(container.exists()).toBe(true);
    expect(container.classes()).not.toContain('interval-pending');
    wrapper.unmount();
  }

  // Provide a job that is *not* "done" so that the component does start incremental refresh...
  {
    const runningInitialJobData = structuredClone(minimalInitialJobData);
    runningInitialJobData.state.run.done = false;
    const wrapper = mount(RepoActionView, {
      props: defaultTestProps,
    });
    await flushPromises();
    const container = wrapper.find('.action-view-container');
    expect(container.exists()).toBe(true);
    expect(container.classes()).toContain('interval-pending');
    wrapper.unmount();
  }
});

test('initial load data is used without calling fetch()', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  const fetchSpy = vi.spyOn(global, 'fetch').mockImplementation((url, _opts) => {
    return Promise.resolve({
      ok: true,
      json: vi.fn().mockResolvedValue(
        url.endsWith('/artifacts') ? minimalInitialArtifactData : minimalInitialJobData,
      ),
    });
  });

  mount(RepoActionView, {
    props: defaultTestProps,
  });
  await flushPromises();
  expect(fetchSpy).not.toHaveBeenCalled();
});

test('view non-picked action run job', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  const wrapper = mount(RepoActionView, {
    props: {
      ...defaultTestProps,
      initialJobData: {
        ...minimalInitialJobData,
        // Definitions here should match the same type of content as the related backend test,
        // "TestActionsViewViewPost/attempt out-of-bounds on non-picked task", so that we're sure we can handle in the
        // UI what the backend will deliver in this case.
        state: {
          run: {
            done: false,
            status: 'waiting',
            commit: {
              pusher: {},
            },
            jobs: [
              {
                id: 161,
                name: 'check-1',
                status: 'waiting',
                canRerun: false,
                duration: '0s',
              },
              {
                id: 162,
                name: 'check-2',
                status: 'waiting',
                canRerun: false,
                duration: '0s',
              },
              {
                id: 163,
                name: 'check-3',
                status: 'waiting',
                canRerun: false,
                duration: '0s',
              },
            ],
          },
          currentJob: {
            title: 'check-1',
            details: ['waiting (locale)'], // locale-specific, not exact match to backend test
            steps: [],
            allAttempts: null,
          },
        },
      },
    },
  });
  await flushPromises();

  expect(wrapper.get('.job-info-header-detail li:first-child').text()).toEqual('waiting (locale)');
  expect(wrapper.get('.job-brief-list .job-brief-item:nth-of-type(1) .job-brief-name').text()).toEqual('check-1');
  expect(wrapper.get('.job-brief-list .job-brief-item:nth-of-type(2) .job-brief-name').text()).toEqual('check-2');
  expect(wrapper.get('.job-brief-list .job-brief-item:nth-of-type(3) .job-brief-name').text()).toEqual('check-3');

  // Attempt status
  expect(wrapper.get('.job-info-header h3').text()).toEqual('check-1');
  expect(wrapper.findAll('ul.job-info-header-detail li').length).toEqual(1);
  expect(wrapper.get('ul.job-info-header-detail li:nth-child(1)').text()).toEqual('waiting (locale)');
});

test('view without pre-execution error', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  const wrapper = mount(RepoActionView, {
    props: defaultTestProps,
  });
  await flushPromises();
  expect(wrapper.find('.pre-execution-error').exists()).toBe(false);
});

test('view with pre-execution error', async () => {
  Object.defineProperty(document.documentElement, 'lang', {value: 'en'});
  const wrapper = mount(RepoActionView, {
    props: {
      ...defaultTestProps,
      initialJobData: {
        ...minimalInitialJobData,
        state: {
          ...minimalInitialJobData.state,
          run: {
            ...minimalInitialJobData.state.run,
            preExecutionError: 'Oops, I dropped it.',
          },
        },
      },
    },
  });
  await flushPromises();
  const block = wrapper.find('.pre-execution-error');
  expect(block.exists()).toBe(true);
  expect(block.text()).toBe('pre-execution error Oops, I dropped it.');
});
