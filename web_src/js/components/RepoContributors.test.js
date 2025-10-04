import {flushPromises, mount} from '@vue/test-utils';
import RepoContributors from './RepoContributors.vue';

test('has commits from before 2001', async () => {
  vi.spyOn(global, 'fetch').mockResolvedValue({
    json: vi.fn().mockResolvedValue({
      'daniel@haxx.se': {
        name: 'Daniel Stenberg',
        total_commits: 13,
        weeks: {
          1754179200000: {
            week: 1754179200000,
            additions: 4330,
            deletions: 47,
            commits: 10,
          },
          946166400000: {
            week: 946166400000,
            additions: 37273,
            deletions: 0,
            commits: 1,
          },
        },
      },
      total: {
        name: 'Total',
        total_commits: 11,
        weeks: {
          1754179200000: {
            week: 1754179200000,
            additions: 4330,
            deletions: 47,
            commits: 10,
          },
          946166400000: {
            week: 946166400000,
            additions: 37273,
            deletions: 0,
            commits: 1,
          },
        },
      },
    }),
    ok: true,
  });

  const repoContributorsGraph = mount(RepoContributors, {
    global: {
      stubs: {
        'relative-time': {
          template: '<span>relative time</span>',
        },
      },
    },
    props: {
      repoLink: '',
      repoDefaultBranchName: '',
      locale: {
        filterLabel: '',
        contributionType: {
          commits: '',
          additions: '',
          deletions: '',
        },

        loadingTitle: '',
        loadingTitleFailed: '',
        loadingInfo: '',
      },
    },
  });
  await flushPromises();

  expect(repoContributorsGraph.componentVM.xAxisStart).toBe(946166400000);
  expect(repoContributorsGraph.componentVM.contributorsStats['daniel@haxx.se'].weeks[0].week).toBe(946166400000);
});
