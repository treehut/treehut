# Treehut CSS layer

Everything in this directory is fork-owned. Upstream Forgejo has no files here,
so nothing here can ever produce a merge conflict when pulling from upstream.
That is the entire point of the directory.

## The layering contract

Each treehut theme is a four-line file that stacks these layers in order:

```css
@import "./theme-gitea-dark.css";      /* 1. upstream base — untouched */
@import "../treehut/ramp-dark.css";    /* 2. raw colour ramps           */
@import "../treehut/semantic.css";     /* 3. role tokens (--th-*)       */
@import "../treehut/bridge.css";       /* 4. upstream tokens ← role tokens */
@import "../treehut/overrides.css";    /* 5. component divergence       */
```

Later layers win, so each one only has to state what it changes.

| Layer | File | Holds | Never holds |
|---|---|---|---|
| 1 | upstream `theme-gitea-*.css` | every token upstream defines | — |
| 2 | `ramp-{dark,light}.css` | the only colour literals in the fork | roles, components |
| 3 | `semantic.css` | `--th-*` role names, mode-agnostic | colour literals |
| 4 | `bridge.css` | upstream `--color-*` ← `--th-*` | colour literals, selectors |
| 5 | `overrides.css` | selectors, layout, spacing | colour literals |

**Layer 1 is a safety net, not a formality.** Because the upstream theme is
imported first, any token we have not mapped still resolves to its upstream
value. A token added upstream cannot break the fork — it simply is not
treehut-tinted until someone maps it in `bridge.css`.

## Why the bridge exists

Forgejo names most colour tokens by ordinal position — `--color-primary-light-5`,
`--color-secondary-dark-8` — and references them from 313 sites across 30+
files. Renaming them at the call sites would mean re-resolving a conflict in
every one of those files on every upstream pull, forever.

So we don't rename them; we redefine them. Upstream keeps its vocabulary and
keeps working untouched, and we author against `--th-*`.

The ordinals are not as arbitrary as they look. Reading upstream's light and
dark themes side by side, one rule explains both:

- `--color-X-light-N` blends X **toward the canvas** (recede, wash out)
- `--color-X-dark-N` blends X **away from the canvas** (assert, stand out)

Which is why gitea-light maps `--color-primary-hover` to `primary-dark-1` while
gitea-dark maps it to `primary-light-1`: both mean "one step further from the
page background". The words "light" and "dark" describe the Sass function that
generated the value, not the direction it moves on screen. `bridge.css` encodes
that rule as a `color-mix()`, which is why 40-odd hand-maintained hex literals
collapsed into two readable ramps.

## Adding a theme variant

A variant is a ramp override, nothing more:

```css
@import "./theme-treehut-dark.css";

:root {
  --th-accent-hue: 250deg;  /* green → blue, safe for deuteranopia */
}
```

Because the accent hue feeds the whole bridge, this repaints buttons, links,
labels and focus rings together rather than requiring a per-component patch.

### Colourblind variants

The six shipped variants are hue overrides and nothing else — status text, icons,
labels, badges **and diff backgrounds** all compose from four hues, so one
declaration moves them together. That is the whole reason the diff family is
bridged: forgejo's own colourblind themes patch only the nine `--color-diff-*`
tokens, which is why they recolour code diffs and leave the rest of the UI
red-green.

Hues were not chosen by eye. `tools/verify-cvd-hues.mjs` simulates dichromacy
(Viénot, Brettel & Mollon 1999) and reports the worst-case OKLab distance between
any two status colours after simulation:

```
base theme                deutan 0.010  tritan 0.009   ← indistinguishable
deuteranopia-protanopia   deutan 0.082  protan 0.105
tritanopia                tritan 0.201
```

Run it after touching any hue. The results are counter-intuitive: the obvious
"keep danger red, make attention yellow" fix scores 0.012 — no better than the
base — because red and yellow collapse together under deuteranopia.

Syntax highlighting is deliberately **not** re-hued by the variants. It
distinguishes about seven categories, more than any CVD-safe palette carries by
hue alone, so one legible palette beats a scrambled one.

## Status families: the remaining gap

Upstream defines these in three overlapping groups that need reconciling into
one semantic family each (`--th-success-*`, `--th-danger-*`, and so on):

- `--color-{red,green,yellow,orange,olive,teal,blue,violet,purple,pink,brown}`
  plus `-light`, `-dark-1`, `-dark-2` variants
- `--color-{success,warning,error,info}-{border,bg,text}`
- `--color-diff-{added,removed,moved}-{word,row}-{bg,border}` and
  `--color-{red,green,yellow,orange}-badge{,-bg,-hover-bg}`

Until then, treehut inherits upstream's versions of all of the above. That is
correct-but-untinted, not broken.

**Done so far:** every token upstream reads through a *fallback*
(`--color-danger-bg`, the five `--color-thin-*`, `--color-label-bg-alt`) plus the
nine `--color-diff-*` tokens. Hue knobs live in the ramps, roles in
`semantic.css`, upstream names in `bridge.css`. Follow that shape for the rest.

**Still inherited from upstream:** the `--color-{success,warning,error,info}-*`
surface families and the named `--color-{red,green,yellow,…}` set. These are the
remaining reason a colourblind variant cannot re-hue *everything* — notably
`--color-blue` / `--color-info-*`, which is why the tritanopia variant carries a
known limitation about accent green reading close to info blue.

### The fallback trap

Some tokens are **defined only by forgejo's themes, while upstream CSS reads them
with a fallback**. A gitea-derived theme like ours takes the fallback and
silently gets a colour pairing nobody designed. `modules/button.css:67-75`:

```css
background: var(--color-danger-bg, var(--color-error-bg));
color:      var(--color-thin-red,  var(--color-red));
&:hover { color: var(--color-thin-red-highlight, var(--color-red)); }
```

Gitea's themes define none of the three, so the danger button rendered mid-red
text on a dark-red fill at **2.80** contrast, and its hover fell back to the same
value as its resting state — no hover feedback at all. Bridged, it measures 5.91.

Note that darkening the *fill* alone could not have fixed this: it asymptotes
around 4.38, at which point the button no longer separates from the canvas. Both
halves of a pairing have to be owned.

To find any remaining instances:

```sh
grep -rhoE 'var\(--color-[a-z0-9-]+, *var\(--color-[a-z0-9-]+\)\)' web_src/css --include='*.css' | sort -u
```

That list should stay empty. If a future upstream pull adds a new one, bridge it
rather than letting the fallback stand.

## Rules of thumb

1. **Colour literals only in `ramp-*.css`.** If you are typing a hex code or an
   `oklch()` anywhere else, it belongs in a ramp.
2. **Prefer `overrides.css` over editing upstream CSS.** `head_style.tmpl`
   loads `index.css` before `theme-*.css`, so equal-specificity rules here win
   with no `!important` and no `[data-theme]` wrapper. If that load order ever
   changes, these rules silently stop applying — check there first.
3. **Do not reformat upstream files.** A Prettier pass over `base.css` and
   friends touches the same lines upstream touches and guarantees conflicts on
   otherwise-clean pulls. This was reverted once already.
4. **Editing upstream CSS is occasionally right** — but only for changes that
   are behaviour-preserving and genuinely upstreamable, such as replacing a
   hardcoded `0.28571429rem` with the `var(--border-radius)` token that already
   exists. Those are small, semantic, and worth offering back to Forgejo.

## Known follow-up

Fomantic's module CSS hardcodes `0.28571429rem` (exactly 4px at the 14px root)
for border radii instead of using upstream's own `--border-radius` token. Until
those call sites are tokenised, `--border-radius: 6px` in `overrides.css` does
not reach them. Note that the sibling values (`0.78571429rem` = 11px,
`0.92857143rem` = 13px, and so on) are all integer pixels over a 14px root, but
the `em`-based ones are element-relative and drive Fomantic's `.small`/`.large`
sizing — converting those to px would break that scaling, so leave them alone.
