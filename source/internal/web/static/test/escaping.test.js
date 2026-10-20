// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Escaping coverage, as a ratchet rather than a proof.
 *
 * The builders interpolate values into `innerHTML` template literals, and
 * correctness depends on `esc()` being applied at each site. Auditing all of them
 * is a separate piece of work, so this test does something narrower and honest:
 *
 *   it records every interpolation that is *not* visibly escaped, in
 *   `escaping.baseline.json`, and fails when that set grows.
 *
 * A new unescaped interpolation therefore fails immediately. The baseline can
 * only shrink — reducing an entry is a one-line edit, and the goal is to drive
 * the number down rather than to declare the file safe.
 *
 * Why not a cleverer rule: deciding whether `${x}` is text needs data flow, and a
 * heuristic broad enough to keep 348 sites quiet would also excuse the one site
 * that matters. A ratchet is what a regex can honestly offer.
 */
const test = require("node:test");
const assert = require("node:assert");
const fs = require("node:fs");
const path = require("node:path");

const ROOT = path.resolve(__dirname, "..");
const BASELINE = path.join(__dirname, "escaping.baseline.json");

const FILES = [
  "js/features/delivery/core.js",
  "js/features/delivery/helpers.js",
  "js/features/delivery/modals.js",
  "js/features/delivery/jobs.js",
  "js/features/delivery/reqs.js",
  "js/features/delivery/versions.js",
  "js/features/delivery/designs.js",
  "js/features/delivery/roadmap.js",
  "js/features/delivery/canvas.js",
  "js/features/delivery/test.js",
  "js/features/delivery/wizard.js",
  "js/features/clusters.js",
  "js/features/workbench.js",
  "js/features/jobhistory.js",
  "js/features/ops.js",
  "js/features/nodes.js",
  "js/features/usage.js",
  "js/shared/badge.js",
  "js/shared/shell.js",
  "js/shared/selection.js",
  "js/shared/stream.js",
  "js/shared/assist.js",
  "js/components/ai_chat.js",
  "js/components/md_preview.js",
  "js/app.js",
  "js/nav.js",
];

/** interpolations returns the ${...} expressions inside backticks. */
function interpolations(src) {
  const out = [];
  let i = 0;
  while (i < src.length) {
    if (src[i] !== "`") {
      i++;
      continue;
    }
    i++;
    while (i < src.length && src[i] !== "`") {
      if (src[i] === "\\") {
        i += 2;
        continue;
      }
      if (src[i] === "$" && src[i + 1] === "{") {
        let depth = 1;
        let j = i + 2;
        while (j < src.length && depth > 0) {
          if (src[j] === "{") depth++;
          else if (src[j] === "}") depth--;
          j++;
        }
        out.push(src.slice(i + 2, j - 1).replace(/\s+/g, " ").trim());
        i = j;
        continue;
      }
      i++;
    }
    i++;
  }
  return out;
}

/** isEscaped reports whether the expression visibly runs through esc(). */
function isEscaped(expr) {
  if (/\besc\s*\(/.test(expr)) return true;
  // Numbers and internal identifiers cannot carry markup. Everything else is
  // counted against the baseline, including things that are probably safe —
  // "probably" is what the baseline is for.
  if (/^-?\d+$/.test(expr)) return true;
  if (/^[A-Za-z_$][\w$]*$/.test(expr) && /^(i|n|j|k|w|h|x|y|major|minor|patch|index|count|len|size)$/.test(expr)) {
    return true;
  }
  // A numeric property read: `.length`, `.major`, `.minor`, `.patch`, `.sort_order`,
  // `.step`, `.id` on a numeric field. These are numbers by construction and cannot
  // carry markup, so they do not belong in the baseline as if they were debt.
  if (/^[A-Za-z_$][\w$.?[\]()'" -]*\.(length|major|minor|patch|sort_order|step|index|count)$/.test(expr)) {
    return true;
  }
  return false;
}

/** unescaped collects the offenders for one file. */
function unescaped(rel) {
  const src = fs.readFileSync(path.join(ROOT, rel), "utf8");
  return interpolations(src).filter((e) => !isEscaped(e));
}

// ---------------------------------------------------------------- generation
if (process.env.UPDATE_ESCAPING_BASELINE === "1") {
  const baseline = {};
  for (const rel of FILES) baseline[rel] = unescaped(rel);
  fs.writeFileSync(BASELINE, JSON.stringify(baseline, null, 2) + "\n");
}

// -------------------------------------------------------------------- check
const baseline = JSON.parse(fs.readFileSync(BASELINE, "utf8"));

for (const rel of FILES) {
  test(`${rel}: no new unescaped interpolation`, () => {
    const known = new Map();
    for (const e of baseline[rel] || []) known.set(e, (known.get(e) || 0) + 1);

    const offenders = [];
    for (const e of unescaped(rel)) {
      const n = known.get(e) || 0;
      if (n > 0) {
        known.set(e, n - 1); // covered by the baseline
      } else {
        offenders.push(e);
      }
    }
    assert.deepStrictEqual(
      offenders.slice(0, 10),
      [],
      `${offenders.length} new unescaped interpolation(s) in ${rel}:\n` +
        offenders
          .slice(0, 10)
          .map((e) => "  ${" + e.slice(0, 70) + "}")
          .join("\n") +
        "\nWrap in this.esc(...), or — if it genuinely cannot carry markup — " +
        "regenerate the baseline with UPDATE_ESCAPING_BASELINE=1 and say why.",
    );
  });
}

test("the ratchet can only shrink", () => {
  // A baseline entry that no longer occurs means someone escaped it. That should
  // fail too: otherwise the file accumulates stale excuses and the number stops
  // meaning anything.
  const stale = [];
  for (const rel of FILES) {
    const now = new Map();
    for (const e of unescaped(rel)) now.set(e, (now.get(e) || 0) + 1);
    for (const e of baseline[rel] || []) {
      const n = now.get(e) || 0;
      if (n > 0) now.set(e, n - 1);
      else stale.push(`${rel}: ${e}`);
    }
  }
  assert.deepStrictEqual(
    stale.slice(0, 10),
    [],
    `${stale.length} baseline entr(ies) no longer occur — regenerate with ` +
      `UPDATE_ESCAPING_BASELINE=1 to record the improvement:\n` +
      stale.slice(0, 10).map((s) => "  " + s.slice(0, 90)).join("\n"),
  );
});
