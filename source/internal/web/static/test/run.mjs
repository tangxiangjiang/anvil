// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.dirname(fileURLToPath(import.meta.url));

function discover(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return discover(full);
    return entry.name.endsWith(".test.js") ? [full] : [];
  });
}

const files = discover(root).sort();
if (!files.length) throw new Error("no *.test.js files found");

const result = spawnSync(process.execPath, ["--test", ...files], {
  cwd: path.resolve(root, ".."),
  stdio: "inherit",
});
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
