// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// @watch start
// templates/repo/editor/edit.tmpl
// web_src/css/features/codeeditor.css
// web_src/js/features/codeeditor.ts
// web_src/js/features/codemirror*
// web_src/js/features/repo-editor.js
// web_src/js/features/repo-settings.js
// @watch end

import {expect, type Page} from '@playwright/test';
import {test} from './utils_e2e.ts';

test.use({user: 'user1'});

async function enterFilename(page: Page, filename: string) {
  const filenameInput = page.getByPlaceholder('Name your file…');
  await filenameInput.fill(filename);
}

async function pressEnter(page: Page) {
  await page.keyboard.press('Enter', {delay: 5});
}

async function type(page: Page, text: string) {
  await page.keyboard.type(text, {delay: 10});
}

async function validate(page: Page, expected: string) {
  await expect(async () => {
    const internal = await page.evaluate(() => Array.from(window.codeEditors)[0].state.doc.toString());
    expect(internal).toStrictEqual(expected);
  }).toPass();
  await expect(page.locator('#edit_area')).toHaveValue(expected);
}

test('New file editor', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await enterFilename(page, `f.txt`);

  const editor = page.locator('.cm-content');

  await editor.click();

  await type(page, 'This');
  await pressEnter(page);
  await validate(page, 'This\n');

  await type(page, 'is');
  await pressEnter(page);
  await validate(page, 'This\nis\n');

  await type(page, 'Frogejo!');
  await validate(page, 'This\nis\nFrogejo!');
});

test('New file with autocomplete and indent', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await enterFilename(page, 'f.html');

  const editor = page.locator('.cm-content');

  await expect(editor).toHaveAttribute('data-language', 'html', {timeout: 3000});

  await editor.click();
  await type(page, '<html>');
  await pressEnter(page);
  await validate(page, '<html>\n  \n</html>');

  await type(page, '<head>');
  await pressEnter(page);
  await validate(page, '<html>\n  <head>\n    \n  </head>\n</html>');

  await type(page, '<title>Frogejo is the future');
  await validate(page, '<html>\n  <head>\n    <title>Frogejo is the future</title>\n  </head>\n</html>');
});

test('Preview for markdown file', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=%23%20Frogejo', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await enterFilename(page, 'f.md');

  const editor = page.locator('.cm-content');
  const preview = page.locator('button[data-tab="preview"]');

  await expect(editor).toHaveAttribute('data-language', 'markdown', {timeout: 3000});

  await preview.click();
  await expect(preview).toHaveClass(/(^|\s)active(\s|$)/);
  await expect(page.getByRole('heading', {name: 'Frogejo'})).toBeVisible();
});

test('Set from query', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=This\\nis\\\\nFrogejo!', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  await validate(page, 'This\nis\\nFrogejo!');
});

test('Search in file', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=This\\nis\\nFrogejo!\\nthIs', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const editor = page.locator('.cm-content');
  const searchField = page.locator('.fj-search input[name="search"]');
  const toggleCase = page.locator('label[for="search_case_sensitive"]');
  const toggleRegex = page.locator('label[for="search_regexp"]');
  const toggleByWord = page.locator('label[for="search_by_word"]');
  const nextButton = page.locator('button[aria-label="Next find"]');

  await validate(page, 'This\nis\nFrogejo!\nthIs');

  await editor.click();

  // Open search
  await page.keyboard.press('ControlOrMeta+F', {delay: 5});
  await expect(searchField).toBeFocused();

  const searchResults = editor.locator('.cm-line > .cm-searchMatch');
  await expect(searchResults).toHaveCount(0);

  await searchField.pressSequentially('Is');
  await expect(searchResults).toHaveCount(3);

  await expect(editor.locator('div:nth-child(1)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await expect(editor.locator('div:nth-child(2)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await nextButton.click();
  await expect(editor.locator('div:nth-child(1)')).toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await expect(editor.locator('div:nth-child(2)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await nextButton.click();
  await expect(editor.locator('div:nth-child(1)')).not.toHaveClass(/(^|\s)cm-activeLine(\s|$)/);
  await expect(editor.locator('div:nth-child(2)')).toHaveClass(/(^|\s)cm-activeLine(\s|$)/);

  await toggleByWord.click();
  await expect(searchResults).toHaveCount(1);

  await toggleCase.click();
  await expect(searchResults).toHaveCount(0);

  await toggleByWord.click();
  await expect(searchResults).toHaveCount(1);

  await toggleRegex.click();
  await expect(searchResults).toHaveCount(1);

  await toggleCase.click();
  await searchField.clear();
  await expect(searchResults).toHaveCount(0);

  await searchField.pressSequentially('^is$');
  await expect(searchResults).toHaveCount(1);

  await page.locator('#editor-find').click();
  await expect(searchResults).toHaveCount(0);
  await expect(searchField).toHaveCount(0);
});

test('Replace in file', async ({page}) => {
  const response = await page.goto('/user2/repo1/_new/master?value=This\\nis\\nFrogejo!\\nthIs', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const editor = page.locator('.cm-content');
  const searchField = page.locator('.fj-search input[name="search"]');
  const replaceField = page.locator('.fj-search input[name="replace"]');

  await validate(page, 'This\nis\nFrogejo!\nthIs');

  await editor.click();

  // Open search
  await page.locator('#editor-find').click();
  await expect(searchField).toBeFocused();

  await searchField.pressSequentially('Is');
  await replaceField.pressSequentially('Blub');

  await page.getByRole('button', {name: 'Replace all'}).click();

  await validate(page, 'ThBlub\nBlub\nFrogejo!\nthBlub');
});

test('Do not open search if search button not available', async ({page}) => {
  const response = await page.goto('/user2/repo1/settings/hooks/git/pre-receive', {waitUntil: 'domcontentloaded'});
  expect(response?.status()).toBe(200);

  const editor = page.locator('.cm-content');
  const searchField = page.locator('.fj-search input[name="search"]');

  await expect(page.locator('#editor-find')).toHaveCount(0);
  await editor.click();

  await page.keyboard.press('ControlOrMeta+F', {delay: 5});
  await expect(searchField).toHaveCount(0);
});
