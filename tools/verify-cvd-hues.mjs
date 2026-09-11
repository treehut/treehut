#!/usr/bin/env node
// Scores treehut's status hues for colour-vision deficiency.
//
// The colourblind theme variants in web_src/css/themes/ are pure hue overrides.
// This script is how those hues were chosen, and how to re-check them if they
// change. It simulates dichromacy (Viénot, Brettel & Mollon 1999) and reports
// the worst-case perceptual distance between any two status colours after
// simulation — if two statuses collapse onto the same colour, that pair is the
// score. Higher is better.
//
//   node tools/verify-cvd-hues.mjs
//
// Rough reading of the numbers: below ~0.02 the two colours are effectively
// identical; ~0.08 is a usable difference; above ~0.15 is comfortable.

const ROLES = ['danger', 'success', 'attention', 'done'];

// Hue sets as shipped. Keep in sync with the theme files.
const VARIANTS = {
  'base (theme-treehut-{light,dark})': {hues: [25, 145, 41, 298], check: ['deutan', 'protan', 'tritan']},
  'deuteranopia-protanopia': {hues: [20, 250, 55, 185], check: ['deutan', 'protan']},
  tritanopia: {hues: [25, 145, 100, 300], check: ['tritan']},
};

// Sampled at the "thin" text weight from ramp-dark.css, the most common place
// two statuses sit side by side (icons and labels in a list).
const L = 0.680;
const C = 0.170;

const gamma = (t) => (t <= 0.0031308 ? 12.92 * t : 1.055 * Math.pow(t, 1 / 2.4) - 0.055);
const clamp = (v) => Math.max(0, Math.min(1, v));

function oklchToLinear(l, c, hDeg) {
  const h = (hDeg * Math.PI) / 180;
  const a = c * Math.cos(h);
  const b = c * Math.sin(h);
  const l_ = l + 0.3963377774 * a + 0.2158037573 * b;
  const m_ = l - 0.1055613458 * a - 0.0638541728 * b;
  const s_ = l - 0.0894841775 * a - 1.2914855480 * b;
  const [L3, M3, S3] = [l_ ** 3, m_ ** 3, s_ ** 3];
  return [
    4.0767416621 * L3 - 3.3077115913 * M3 + 0.2309699292 * S3,
    -1.2684380046 * L3 + 2.6097574011 * M3 - 0.3413193965 * S3,
    -0.0041960863 * L3 - 0.7034186147 * M3 + 1.7076147010 * S3,
  ];
}

function linearToOklab(r, g, b) {
  const l = Math.cbrt(Math.max(0, 0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b));
  const m = Math.cbrt(Math.max(0, 0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b));
  const s = Math.cbrt(Math.max(0, 0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b));
  return [
    0.2104542553 * l + 0.7936177850 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.4285922050 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.8086757660 * s,
  ];
}

const hex = (lin) => `#${lin.map((v) => Math.round(clamp(gamma(clamp(v))) * 255).toString(16).padStart(2, '0')).join('')}`;

// Viénot 1999: project onto the dichromat's reduced colour plane in LMS.
function simulate(lin, type) {
  const [r, g, b] = lin.map(clamp);
  let l = 17.8824 * r + 43.5161 * g + 4.11935 * b;
  let m = 3.45565 * r + 27.1554 * g + 3.86714 * b;
  let s = 0.0299566 * r + 0.184309 * g + 1.46709 * b;
  if (type === 'protan') l = 2.02344 * m - 2.52581 * s;
  else if (type === 'deutan') m = 0.494207 * l + 1.24827 * s;
  else if (type === 'tritan') s = -0.395913 * l + 0.801109 * m;
  return [
    0.0809444479 * l - 0.130504409 * m + 0.116721066 * s,
    -0.0102485335 * l + 0.0540193266 * m - 0.113614708 * s,
    -0.000365296938 * l - 0.00412161469 * m + 0.693511405 * s,
  ];
}

function distance(a, b, type) {
  const A = linearToOklab(...(type ? simulate(a, type) : a));
  const B = linearToOklab(...(type ? simulate(b, type) : b));
  return Math.hypot(A[0] - B[0], A[1] - B[1], A[2] - B[2]);
}

function worstPair(hues, type) {
  const cols = hues.map((h) => oklchToLinear(L, C, h));
  let worst = Infinity;
  let which = '';
  for (let i = 0; i < cols.length; i++) {
    for (let j = i + 1; j < cols.length; j++) {
      const d = distance(cols[i], cols[j], type);
      if (d < worst) {
        worst = d;
        which = `${ROLES[i]}/${ROLES[j]}`;
      }
    }
  }
  return {worst, which};
}

for (const [name, {hues, check}] of Object.entries(VARIANTS)) {
  console.log(`\n${name}`);
  console.log(`  hues: ${ROLES.map((r, i) => `${r} ${hues[i]}°`).join(', ')}`);
  const normal = worstPair(hues, null);
  console.log(`  normal vision  worst pair ${normal.worst.toFixed(3)}  (${normal.which})`);
  for (const type of check) {
    const {worst, which} = worstPair(hues, type);
    const verdict = worst < 0.02 ? 'INDISTINGUISHABLE' : worst < 0.08 ? 'marginal' : 'ok';
    console.log(`  ${type.padEnd(14)} worst pair ${worst.toFixed(3)}  (${which})  ${verdict}`);
  }
  for (const [i, h] of hues.entries()) {
    const c = oklchToLinear(L, C, h);
    const sims = check.map((t) => `${t} ${hex(simulate(c, t))}`).join('  ');
    console.log(`    ${ROLES[i].padEnd(10)} ${hex(c)}  →  ${sims}`);
  }
}
console.log();
