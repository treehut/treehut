#!/usr/bin/env node
// Vendors the Catppuccin VS Code icon set for the repository file list.
//
//   node tools/fetch-catppuccin-icons.mjs
//   make svg          # then regenerate public/assets/img/svg
//
// Source: https://github.com/catppuccin/vscode-icons (MIT).
//
// We take the `icons/css-variables/` variant, not one of the four baked flavours
// (latte/frappe/macchiato/mocha). Those icons reference `var(--vscode-ctp-*)`
// rather than hardcoded hex, so the palette is remappable from CSS — see
// web_src/css/treehut/catppuccin.css, which maps those variables onto treehut
// tokens. That is what lets the icons be re-tinted to match the theme instead of
// dragging a second, unrelated palette into the UI.
//
// Writes:
//   web_src/svg/catppuccin/*.svg        vendored sources (picked up by make svg)
//   modules/base/catppuccin_icons.go    generated name -> icon lookup tables
//
// Both outputs are generated. Do not hand-edit them; change this script and
// re-run, so a future refresh from upstream does not silently drop local edits.

import {mkdir, writeFile, rm} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {exit} from 'node:process';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';

const run = promisify(execFile);

const REPO = 'catppuccin/vscode-icons';
const REF = 'main';
const RAW = `https://raw.githubusercontent.com/${REPO}/${REF}`;
const root = (p) => fileURLToPath(new URL(`../${p}`, import.meta.url));
const ICON_DIR = root('web_src/svg/catppuccin');
const GO_OUT = root('modules/base/catppuccin_icons.go');

// VS Code chrome that has no analogue in a repository file listing.
const SKIP = new Set(['_root', '_root_open']);

/** `_folder_open` -> `folder-open`, `folder_src` -> `folder-src`, `_file` -> `file` */
function normalise(name) {
  return name.replace(/_/g, '-').replace(/^-+/, '');
}

async function getJSON(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} for ${url}`);
  return res.json();
}

async function getText(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText} for ${url}`);
  return res.text();
}

/** Evaluate the object literal out of one of catppuccin's TypeScript defaults. */
async function loadDefaults(file, constName) {
  const src = await getText(`${RAW}/src/defaults/${file}`);
  const start = src.indexOf('{', src.indexOf(`const ${constName}`));
  if (start === -1) throw new Error(`could not find "const ${constName}" in ${file}`);
  // Walk braces to find the end of the literal; the file continues with a
  // reduce() and an export that we do not want.
  let depth = 0;
  let end = -1;
  for (let i = start; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}') {
      depth--;
      if (depth === 0) {
        end = i + 1;
        break;
      }
    }
  }
  if (end === -1) throw new Error(`unbalanced braces in ${file}`);
  // The literal is plain JS once the type annotation is gone.
  return new Function(`return ${src.slice(start, end)}`)();
}

async function downloadAll(names, concurrency = 16) {
  const queue = [...names];
  let done = 0;
  const failures = [];
  const worker = async () => {
    for (;;) {
      const name = queue.pop();
      if (name === undefined) return;
      try {
        const svg = await getText(`${RAW}/icons/css-variables/${name}.svg`);
        await writeFile(`${ICON_DIR}/${normalise(name)}.svg`, svg);
      } catch (err) {
        failures.push(`${name}: ${err.message}`);
      }
      done++;
      if (done % 100 === 0) process.stderr.write(`  ${done}/${names.length}\n`);
    }
  };
  await Promise.all(Array.from({length: concurrency}, worker));
  return failures;
}

function goStringMap(name, comment, entries) {
  const lines = [`// ${comment}`, `var ${name} = map[string]string{`];
  for (const key of Object.keys(entries).sort()) {
    lines.push(`\t${JSON.stringify(key)}: ${JSON.stringify(entries[key])},`);
  }
  lines.push('}', '');
  return lines.join('\n');
}

async function main() {
  process.stderr.write('fetching icon list…\n');
  const tree = await getJSON(`https://api.github.com/repos/${REPO}/git/trees/${REF}?recursive=1`);
  if (tree.message) throw new Error(`GitHub API: ${tree.message}`);

  const available = tree.tree
    .filter((e) => e.type === 'blob' && /^icons\/css-variables\/.+\.svg$/.test(e.path))
    .map((e) => e.path.split('/').pop().replace(/\.svg$/, ''))
    .filter((n) => !SKIP.has(n));

  const bytes = tree.tree
    .filter((e) => e.type === 'blob' && /^icons\/css-variables\/.+\.svg$/.test(e.path))
    .reduce((a, e) => a + (e.size || 0), 0);
  process.stderr.write(`  ${available.length} icons, ${(bytes / 1024).toFixed(0)} KiB\n`);

  process.stderr.write('fetching associations…\n');
  const [fileIcons, folderIcons] = await Promise.all([
    loadDefaults('fileIcons.ts', 'fileIcons'),
    loadDefaults('folderIcons.ts', 'folderIcons'),
  ]);

  const have = new Set(available);
  const extensions = {};
  const filenames = {};
  const folders = {};
  const missing = new Set();

  for (const [icon, assoc] of Object.entries(fileIcons)) {
    if (!have.has(icon)) {
      missing.add(icon);
      continue;
    }
    const svg = `ctp-${normalise(icon)}`;
    for (const ext of assoc.fileExtensions ?? []) extensions[ext.toLowerCase()] = svg;
    for (const fn of assoc.fileNames ?? []) filenames[fn.toLowerCase()] = svg;
  }

  for (const [icon, assoc] of Object.entries(folderIcons)) {
    const withPrefix = `folder_${icon}`;
    if (!have.has(withPrefix)) {
      missing.add(withPrefix);
      continue;
    }
    const svg = `ctp-${normalise(withPrefix)}`;
    for (const fn of assoc.folderNames ?? []) folders[fn.toLowerCase()] = svg;
  }

  process.stderr.write(`  ${Object.keys(extensions).length} extensions, ` +
    `${Object.keys(filenames).length} filenames, ${Object.keys(folders).length} folder names\n`);
  if (missing.size) {
    process.stderr.write(`  note: ${missing.size} associations reference icons not in ` +
      `css-variables/ and were skipped\n`);
  }

  process.stderr.write('downloading icons…\n');
  await rm(ICON_DIR, {recursive: true, force: true});
  await mkdir(ICON_DIR, {recursive: true});
  const failures = await downloadAll(available);
  if (failures.length) {
    process.stderr.write(`\n${failures.length} download(s) failed:\n${failures.join('\n')}\n`);
    return 1;
  }

  const header = `// Code generated by tools/fetch-catppuccin-icons.mjs; DO NOT EDIT.
//
// Icon associations from github.com/${REPO} (MIT), which maps file extensions,
// exact filenames and folder names onto icon basenames. Values are svg names as
// registered by tools/generate-svg.js, i.e. the "ctp-" prefix plus the icon's
// basename with underscores turned into hyphens.

package base

`;

  await writeFile(GO_OUT, header +
    goStringMap('catppuccinByExtension', 'File extension (lowercase, no dot) -> icon.', extensions) + '\n' +
    goStringMap('catppuccinByFilename', 'Exact filename (lowercase) -> icon. Takes precedence over the extension.', filenames) + '\n' +
    goStringMap('catppuccinByFolder', 'Exact directory name (lowercase) -> icon.', folders));

  // Align the generated maps the way gofmt would, so re-running this script
  // never leaves the tree dirty and `make lint-go` stays quiet.
  try {
    await run('gofmt', ['-w', GO_OUT]);
  } catch (err) {
    process.stderr.write(`warning: could not run gofmt (${err.message}); ` +
      `run "gofmt -w ${GO_OUT}" by hand\n`);
  }

  process.stderr.write(`\nwrote ${available.length} icons to web_src/svg/catppuccin/\n`);
  process.stderr.write(`wrote ${GO_OUT.split('/').slice(-3).join('/')}\n`);
  process.stderr.write('now run: make svg\n');
  return 0;
}

try {
  exit(await main());
} catch (err) {
  console.error(err);
  exit(1);
}
