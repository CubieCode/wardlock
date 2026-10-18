// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

// Creates synthetic sites and deploys through the API as the signed-in
// owner, the way the CLI will, so the specs have something to show.
import { createHash } from 'node:crypto';
import { expect, type APIRequestContext } from '@playwright/test';

const origin = process.env.WARDLOCK_ACCEPTANCE_ORIGIN!;
const headers = { Origin: origin };

export async function createSite(request: APIRequestContext, name: string) {
  const res = await request.post(`${origin}/api/v0/sites`, {
    data: { name },
    headers,
  });
  expect(res.status()).toBe(201);
}

/** Uploads a one-page deploy saying `text` and makes it live. */
export async function publish(
  request: APIRequestContext,
  site: string,
  text: string,
) {
  const body = Buffer.from(`<!doctype html><title>${text}</title><p>${text}`);
  const sha256 = createHash('sha256').update(body).digest('hex');
  const base = `${origin}/api/v0/sites/${site}/deploys`;
  const created = await request.post(base, {
    data: {
      message: text,
      files: [{ path: 'index.html', sha256, size: body.length }],
    },
    headers,
  });
  expect(created.status()).toBe(201);
  const { deploy, missing } = (await created.json()) as {
    deploy: { id: string };
    missing: string[];
  };
  for (const hash of missing) {
    const put = await request.put(`${base}/${deploy.id}/blobs/${hash}`, {
      data: body,
      headers: { ...headers, 'Content-Type': 'application/octet-stream' },
    });
    expect(put.status()).toBe(204);
  }
  const activated = await request.post(`${base}/${deploy.id}/activate`, {
    headers,
  });
  expect(activated.status()).toBe(200);
  return deploy.id;
}

export async function protect(request: APIRequestContext, site: string) {
  const res = await request.put(`${origin}/api/v0/sites/${site}/policies`, {
    data: { path: '/', visibility: 'protected', methods: ['links'] },
    headers,
  });
  expect(res.status()).toBe(200);
}
