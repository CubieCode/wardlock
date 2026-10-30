// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Runs one of the action's step scripts as the runner would: bash, the
// step's env, and a GITHUB_OUTPUT file read back as an object.
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

export const scripts = resolve(import.meta.dirname, '../scripts');

/** @returns {string} a new empty temporary directory */
export const tempDir = () => mkdtempSync(join(tmpdir(), 'wardlock-action-'));

/**
 * Runs scripts/<name> asynchronously (so an in-process fake server can
 * answer it) and resolves with its status, output and step outputs.
 * @param {string} name
 * @param {Record<string, string>} env
 * @param {{ cwd?: string }} [options]
 */
export function runStep(name, env, options = {}) {
  const dir = tempDir();
  const output = join(dir, 'github-output');
  writeFileSync(output, '');
  return new Promise((done, failed) => {
    const child = spawn('bash', [join(scripts, name)], {
      cwd: options.cwd ?? dir,
      env: {
        PATH: process.env.PATH ?? '',
        HOME: dir,
        RUNNER_TEMP: dir,
        GITHUB_OUTPUT: output,
        ...env,
      },
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    child.on('error', failed);
    child.on('close', (status) => {
      /** @type {Record<string, string>} */
      const outputs = {};
      for (const line of readFileSync(output, 'utf8').split('\n')) {
        const eq = line.indexOf('=');
        if (eq > 0) outputs[line.slice(0, eq)] = line.slice(eq + 1);
      }
      done({ status, stdout, stderr, outputs });
    });
  });
}
