// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// A fake of the few GitHub REST endpoints the action's comment step uses:
// listing, creating and updating issue comments. comment.test.mjs imports
// it; the action's CI job runs it as a program, which listens on loopback,
// prints its URL and writes every comment to STATE_FILE after each change:
//
//   node fake-github.mjs TOKEN STATE_FILE
import { writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { pathToFileURL } from 'node:url';

/**
 * @typedef {{ id: number, issue: number, body: string, user: { login: string, type: string }, updates: number }} Comment
 * @typedef {{ method: string, url: string }} Seen
 */

/**
 * Starts the fake. Requests without `Bearer <token>` answer 401; with
 * `forbid` set, writes answer 403 as GitHub does for a token without
 * pull-requests write.
 * @param {{ token: string, forbid?: boolean, onChange?: (comments: Comment[]) => void }} options
 */
export async function startFakeGitHub({ token, forbid = false, onChange }) {
  /** @type {Comment[]} */
  const comments = [];
  /** @type {Seen[]} */
  const requests = [];
  let nextId = 1000;

  const server = createServer((req, res) => {
    let raw = '';
    req.on('data', (chunk) => (raw += chunk));
    req.on('end', () => {
      const url = new URL(req.url ?? '/', 'http://fake');
      requests.push({
        method: req.method ?? '',
        url: url.pathname + url.search,
      });
      /** @param {number} status @param {unknown} body */
      const send = (status, body) => {
        res.writeHead(status, { 'content-type': 'application/json' });
        res.end(JSON.stringify(body));
      };
      if (req.headers.authorization !== `Bearer ${token}`) {
        return send(401, { message: 'Bad credentials' });
      }
      const list = /^\/repos\/[^/]+\/[^/]+\/issues\/(\d+)\/comments$/.exec(
        url.pathname,
      );
      const one = /^\/repos\/[^/]+\/[^/]+\/issues\/comments\/(\d+)$/.exec(
        url.pathname,
      );
      if (list && req.method === 'GET') {
        const perPage = Number(url.searchParams.get('per_page') ?? 30);
        const page = Number(url.searchParams.get('page') ?? 1);
        const mine = comments.filter((c) => c.issue === Number(list[1]));
        return send(200, mine.slice((page - 1) * perPage, page * perPage));
      }
      if ((list && req.method === 'POST') || (one && req.method === 'PATCH')) {
        if (forbid) {
          return send(403, {
            message: 'Resource not accessible by integration',
          });
        }
        const { body } = JSON.parse(raw);
        if (list) {
          const c = {
            id: nextId++,
            issue: Number(list[1]),
            body,
            user: { login: 'github-actions[bot]', type: 'Bot' },
            updates: 0,
          };
          comments.push(c);
          onChange?.(comments);
          return send(201, c);
        }
        const c = comments.find((x) => x.id === Number(one?.[1]));
        if (!c) return send(404, { message: 'Not Found' });
        c.body = body;
        c.updates++;
        onChange?.(comments);
        return send(200, c);
      }
      send(404, { message: 'Not Found' });
    });
  });
  await new Promise((done) => server.listen(0, '127.0.0.1', () => done(null)));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('no address');
  return {
    url: `http://127.0.0.1:${address.port}`,
    comments,
    requests,
    close: () => new Promise((done) => server.close(() => done(null))),
  };
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) {
  const [token, stateFile] = process.argv.slice(2);
  if (!token || !stateFile) {
    console.error('usage: node fake-github.mjs TOKEN STATE_FILE');
    process.exit(2);
  }
  writeFileSync(stateFile, '[]\n');
  const fake = await startFakeGitHub({
    token,
    onChange: (comments) =>
      writeFileSync(stateFile, JSON.stringify(comments, null, 2) + '\n'),
  });
  console.log(fake.url);
}
