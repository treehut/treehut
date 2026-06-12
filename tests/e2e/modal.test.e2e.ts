// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/demo/modal.tmpl
// templates/repo/editor/edit.tmpl
// templates/repo/editor/patch.tmpl
// web_src/js/features/repo-editor.js
// web_src/css/modules/dialog.ts
// web_src/css/modules/dialog.css
// @watch end

import {expect} from '@playwright/test';
import {dynamic_id, test} from './utils_e2e.ts';
import {screenshot} from './shared/screenshots.ts';

test.use({user: 'user2'});

test('Dialog modal', async ({page}) => {
  let response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const filename = `${dynamic_id()}.md`;

  await page.getByPlaceholder('Name your file…').fill(filename);
  await page.locator('.cm-content').click();
  await page.keyboard.type('Hi, nice to meet you. Can I talk about ');

  await page.locator('.quick-pull-choice input[value="direct"]').click();
  await page.getByRole('button', {name: 'Commit changes'}).click();
  await expect(page).toHaveURL(`/user2/repo1/src/branch/master/${filename}`);

  response = await page.goto(`/user2/repo1/_edit/master/${filename}`, {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await page.locator('.cm-content').click();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.press('Backspace');

  await page.locator('#commit-button').click();
  await screenshot(page);
  await expect(page.locator('#edit-empty-content-modal')).toBeVisible();

  await page.locator('#edit-empty-content-modal .cancel').click();
  await expect(page.locator('#edit-empty-content-modal')).toBeHidden();

  await page.locator('#commit-button').click();
  await page.locator('#edit-empty-content-modal .ok').click();
  await expect(page).toHaveURL(`/user2/repo1/src/branch/master/${filename}`);
});

test('Dialog modal: width', async ({page, isMobile}) => {
  // This test doesn't need JS and runs a little faster without it
  await page.goto('/-/demo/modal');

  // Open modal with short content
  const shortModal = page.locator('#short-modal');
  await expect(shortModal).toBeHidden();
  await page.locator('button[data-modal="#short-modal"]').click();
  await expect(shortModal).toBeVisible();

  // Check it's width
  let width = Math.round((await shortModal.boundingBox()).width);
  if (isMobile) {
    // Bound by viewport width
    expect(width).toBeLessThan(400);
  } else {
    // Bound by min-width
    expect(width).toBe(400);
  }

  // Open modal with medium sized content
  await shortModal.locator('button.cancel').click();
  const mediumModal = page.locator('#medium-modal');
  await expect(mediumModal).toBeHidden();
  await page.locator('button[data-modal="#medium-modal"]').click();
  await expect(mediumModal).toBeVisible();

  // Check it's width
  width = Math.round((await mediumModal.boundingBox()).width);
  if (isMobile) {
    // Bound by viewport width
    expect(width).toBeLessThan(400);
  } else {
    // Not bound by min-width or max-width
    expect(width).toBeLessThan(800);
    expect(width).toBeGreaterThan(400);
  }

  // Open modal with long content
  await mediumModal.locator('button.cancel').click();
  const longModal = page.locator('#long-modal');
  await expect(longModal).toBeHidden();
  await page.locator('button[data-modal="#long-modal"]').click();
  await expect(longModal).toBeVisible();

  // Check it's width
  width = Math.round((await longModal.boundingBox()).width);
  if (isMobile) {
    // Bound by viewport width
    expect(width).toBeLessThan(400);
  } else {
    // Bound by max-width
    expect(width).toBe(800);
  }
});

test('Dialog modal: short viewport', async ({page, isMobile}) => {
  test.skip(isMobile);

  // Small height for viewport.
  await page.setViewportSize({
    width: 1000,
    height: 200,
  });

  await page.goto('/user2/repo1/settings');

  // Open modal with long content
  const deleteModal = page.locator('#delete-repo-modal');
  await expect(deleteModal).toBeHidden();
  await page.getByRole('button', {name: 'Delete this repository'}).click();
  await expect(deleteModal).toBeVisible();

  // Scroll to the bottom.
  const scrollY = await page.evaluate(() => document.querySelector('.ui.dimmer').scrollHeight);
  await page.mouse.wheel(0, scrollY);
  const scrollTop = await page.evaluate(() => document.querySelector('.ui.dimmer').scrollTop);
  expect(scrollTop).toBeGreaterThan(0);
});
