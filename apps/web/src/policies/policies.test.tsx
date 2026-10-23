// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { App } from '../app.js';
import type { Policy, Site } from '../sites/format.js';
import {
  cleanPath,
  governing,
  parseEntries,
  requestPath,
  validOrg,
} from './format.js';
import { PoliciesCard } from './policies.js';

const me = {
  owner: {
    id: '01928c5e-7a1b-7c3d-9e4f-5a6b7c8d9e00',
    email: 'owner@example.test',
    createdAt: 1767225600000,
  },
  credential: 'session',
  scopes: ['deploy', 'manage'],
};

const now = Date.now();

const docs: Site = {
  id: '01928c5e-7a1b-7c3d-9e4f-000000000001',
  name: 'docs',
  hostname: 'docs.sites.example.test',
  url: 'https://docs.sites.example.test/',
  createdAt: now,
  unavailable: false,
  tagIds: [],
  pinned: false,
};

function policy(path: string, extra: Partial<Policy> = {}): Policy {
  return {
    id: `01928c5e-7a1b-7c3d-9e4f-0000${path.replace(/\W/g, 'x').padEnd(8, '0')}`,
    path,
    visibility: 'public',
    methods: [],
    allowlist: { emails: [], domains: [], githubOrgs: [] },
    version: 1,
    createdAt: now,
    updatedAt: now,
    ...extra,
  };
}

// A synthetic password; it must never reach the page.
const password = 'harbour-lights-7';

type Handler = (request: Request, body: unknown) => Response;

function fakeServer(start: Policy[], routes: Record<string, Handler> = {}) {
  let policies = [...start];
  const calls: { route: string; body: unknown }[] = [];
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const url = new URL(request.url);
      const route = `${request.method} ${url.pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route: route + url.search, body });
      if (routes[route]) return routes[route](request, body);
      if (route === 'GET /api/v0/me') return Response.json(me);
      if (route === 'GET /api/v0/sites/docs') return Response.json(docs);
      if (route === 'GET /api/v0/sites/docs/deploys')
        return Response.json({ items: [] });
      if (route === 'GET /api/v0/sites/docs/links')
        return Response.json({ items: [] });
      if (route === 'GET /api/v0/sites/docs/policies')
        return Response.json({ items: policies });
      if (route === 'PUT /api/v0/sites/docs/policies') {
        // Like the server: never echo the password back.
        const req = body as {
          path: string;
          visibility: Policy['visibility'];
          methods?: Policy['methods'];
          allowlist?: Policy['allowlist'];
        };
        const old = policies.find((p) => p.path === req.path);
        const saved = policy(req.path, {
          ...(old ? { id: old.id } : {}),
          visibility: req.visibility,
          methods: req.methods ?? ['links'],
          allowlist:
            req.visibility === 'public'
              ? { emails: [], domains: [], githubOrgs: [] }
              : {
                  emails: req.allowlist?.emails ?? old?.allowlist.emails ?? [],
                  domains:
                    req.allowlist?.domains ?? old?.allowlist.domains ?? [],
                  githubOrgs:
                    req.allowlist?.githubOrgs ??
                    old?.allowlist.githubOrgs ??
                    [],
                },
          version: (old?.version ?? 0) + 1,
        });
        policies = [...policies.filter((p) => p.path !== req.path), saved];
        return Response.json(saved);
      }
      const deleted = /^DELETE \/api\/v0\/sites\/docs\/policies\/(.+)$/.exec(
        route,
      );
      if (deleted) {
        policies = policies.filter((p) => p.id !== deleted[1]);
        return new Response(null, { status: 204 });
      }
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return {
    api,
    calls,
    puts: () => calls.filter((c) => c.route.startsWith('PUT')),
  };
}

const card = async (api: ReturnType<typeof fakeServer>['api']) => {
  render(<PoliciesCard api={api} site={docs} />);
  return screen.findByRole('list', { name: 'Access policies' });
};

const dialog = (name: string | RegExp) => screen.getByRole('dialog', { name });

/** Fails if the password is anywhere in the document. */
function expectNoPassword() {
  expect(document.body.innerHTML).not.toContain(password);
  for (const input of document.querySelectorAll('input'))
    expect(input.value).not.toBe(password);
}

afterEach(cleanup);

describe('governing', () => {
  const policies = [
    policy('/', { visibility: 'protected', methods: ['links'] }),
    policy('/docs/'),
    policy('/docs/internal/', { visibility: 'protected', methods: ['email'] }),
  ];
  it.each([
    ['/', '/'],
    ['/index.html', '/'],
    ['/docs', '/docs/'],
    ['/docs/', '/docs/'],
    ['/docs/guide.html', '/docs/'],
    ['/docs/internal', '/docs/internal/'],
    ['/docs/internal/plan.html', '/docs/internal/'],
    ['/docsx/page.html', '/'],
    ['/Docs/guide.html', '/'],
  ])('%s follows %s', (path, expected) => {
    expect(governing(policies, path)?.path).toBe(expected);
  });
});

// The edge's cleanPath (apps/server/internal/edge/edge.go): Go's path.Clean
// on the rooted path, with a trailing slash kept. Cases from edge_test.go,
// siteaccess_test.go and accesslog_test.go, plus path.Clean's own rules.
describe('cleanPath', () => {
  it.each([
    ['', '/'],
    ['/', '/'],
    ['about', '/about'],
    ['//about', '/about'],
    ['//about/', '/about/'],
    ['/assets/../guide', '/guide'],
    ['/assets/../about/', '/about/'],
    ['/assets/../.wardlock/index.html', '/.wardlock/index.html'],
    ['//.wardlock/health', '/.wardlock/health'],
    ['/docs/../.wardlock/health', '/.wardlock/health'],
    ['/x/../.wardlock/tls-ask', '/.wardlock/tls-ask'],
    ['/docs/../admin/x', '/admin/x'],
    ['/docs/./guide.html', '/docs/guide.html'],
    ['/docs/.', '/docs'],
    ['/docs/..', '/'],
    ['/../../etc', '/etc'],
    ['/a//b///c/', '/a/b/c/'],
    ['/./', '/'],
  ])('%j cleans to %j', (raw, clean) => {
    expect(cleanPath(raw)).toBe(clean);
  });

  it('decodes a typed request path before cleaning it', () => {
    expect(requestPath(' /docs/%2E%2E/admin/x?y=1#z ')).toBe('/admin/x');
    expect(requestPath('/caf%C3%A9/')).toBe('/café/');
    expect(requestPath('/100%/x')).toBe('/100%/x');
  });
});

describe('validOrg', () => {
  it.each([
    ['example-org', true],
    ['Example-Org', true],
    ['a', true],
    ['a'.repeat(39), true],
    ['a'.repeat(40), false],
    ['-org', false],
    ['org-', false],
    ['ex--org', false],
    ['ex_org', false],
    ['ex.org', false],
  ])('%j is %s', (org, ok) => {
    expect(validOrg(org)).toBe(ok);
  });
});

describe('parseEntries', () => {
  it('splits addresses from domains on lines, commas and spaces', () => {
    expect(
      parseEntries('ana@example.test\nexample.org, *.example.net ;  bo@x.test'),
    ).toEqual({
      emails: ['ana@example.test', 'bo@x.test'],
      domains: ['example.org', '*.example.net'],
    });
  });
});

describe('access policies card', () => {
  it('lists the default first with each path policy, its ways in and allowlist', async () => {
    const { api } = fakeServer([
      policy('/', {
        visibility: 'protected',
        methods: ['links', 'email', 'oauth'],
        allowlist: {
          emails: ['ana@example.test'],
          domains: ['example.org', 'example.net'],
          githubOrgs: ['example-org'],
        },
      }),
      policy('/assets/'),
    ]);
    const list = await card(api);
    const [first, second] = within(list).getAllByRole('listitem');
    expect(first!.textContent).toContain('Site default');
    expect(first!.textContent).toContain('Protected');
    expect(first!.textContent).toContain(
      'Ways in: share links, email code and GitHub, Google or single sign-on.',
    );
    expect(first!.textContent).toContain(
      'Allowlist: ana@example.test, example.org and 2 more.',
    );
    expect(
      within(first!).queryByRole('button', { name: /^Delete/ }),
    ).toBeNull();
    expect(second!.textContent).toContain('/assets/');
    expect(second!.textContent).toContain('Public');
    expect(second!.textContent).toContain(
      'Covers /assets/ and everything under it.',
    );
    expect(
      within(second!).getByRole('button', {
        name: 'Delete the policy for /assets/',
      }),
    ).toBeTruthy();
    // How precedence works is said on the card.
    expect(
      screen.getByText(/longest path that matches it/).textContent,
    ).toContain('Policies do not combine');
  });

  it('names each visibility with the site page’s access word', async () => {
    const { api } = fakeServer([
      policy('/', { visibility: 'protected', methods: ['password'] }),
      policy('/assets/'),
    ]);
    await card(api);
    const rows = within(
      screen.getByRole('list', { name: 'Access policies' }),
    ).getAllByRole('listitem');
    // The ward marks Protected and the public colour marks Public, as in the
    // header and on the Overview.
    const word = (row: HTMLElement, text: string) =>
      within(row).getByText(text, { selector: '.wl-status-tag' });
    expect(word(rows[0]!, 'Protected').className).toContain(
      'wl-status-tag-ward',
    );
    expect(word(rows[0]!, 'Protected').querySelector('.wl-ward')).toBeTruthy();
    expect(word(rows[1]!, 'Public').className).toContain(
      'wl-status-tag-public',
    );
    fireEvent.change(screen.getByRole('textbox', { name: 'Check a path' }), {
      target: { value: '/assets/x' },
    });
    expect(
      document.querySelector('.policy-check-result .wl-status-tag-public'),
    ).toBeTruthy();
  });

  it('says which policy decides a typed path', async () => {
    const { api } = fakeServer([
      policy('/', { visibility: 'protected', methods: ['password'] }),
      policy('/assets/'),
    ]);
    await card(api);
    const field = screen.getByRole('textbox', { name: 'Check a path' });
    const result = () =>
      document.querySelector('.policy-check-result')!.textContent;
    fireEvent.change(field, { target: { value: 'assets/logo.svg' } });
    expect(result()).toBe(
      '/assets/logo.svg follows the policy for /assets/: Public.',
    );
    fireEvent.change(field, { target: { value: '/assetsx?x=1' } });
    expect(result()).toBe(
      '/assetsx follows the site default: Protected (password).',
    ); // Cleaned as the server cleans it, so dot segments cannot borrow
    // another folder's policy.
    fireEvent.change(field, { target: { value: '/assets/../admin/x' } });
    expect(result()).toBe(
      '/admin/x follows the site default: Protected (password).',
    );
    fireEvent.change(field, { target: { value: '//assets//logo.svg' } });
    expect(result()).toBe(
      '/assets/logo.svg follows the policy for /assets/: Public.',
    );
    fireEvent.change(field, { target: { value: '/x/../.Wardlock/login' } });
    expect(result()).toBe(
      '/.Wardlock/login is one of Wardlock’s own pages, which no policy covers.',
    );
  });

  it('sets a password without ever showing it again', async () => {
    const { api, puts } = fakeServer([policy('/')]);
    await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    fireEvent.click(within(edit).getByRole('radio', { name: 'Protected' }));
    fireEvent.click(within(edit).getByRole('checkbox', { name: 'Password' }));
    const field = within(edit).getByLabelText('Visitor password');
    expect(field.getAttribute('type')).toBe('password');
    expect(field.getAttribute('autocomplete')).toBe('new-password');

    fireEvent.change(field, { target: { value: 'short' } });
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(within(edit).getByRole('alert').textContent).toBe(
      'Use at least 8 characters.',
    );
    expect(puts()).toHaveLength(0);

    fireEvent.change(field, { target: { value: password } });
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    await screen.findByText(/Saved the site default/);
    expect(puts()[0]!.body).toEqual({
      path: '/',
      visibility: 'protected',
      methods: ['links', 'password'],
      password,
    });
    expect(screen.queryByRole('dialog')).toBeNull();
    expectNoPassword();

    // Editing again starts blank; leaving it blank keeps the password.
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const again = dialog('Edit the site default for docs');
    const kept = within(again).getByLabelText('New visitor password');
    expect((kept as HTMLInputElement).value).toBe('');
    expect(
      within(again).getByText(/A password is set. Leave this blank to keep it/),
    ).toBeTruthy();
    fireEvent.click(within(again).getByRole('button', { name: 'Save policy' }));
    await waitFor(() => expect(puts()).toHaveLength(2));
    expect(puts()[1]!.body).not.toHaveProperty('password');
    expectNoPassword();
  });

  it('needs at least one way in and a password the first time', async () => {
    const { api, puts } = fakeServer([policy('/')]);
    await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    fireEvent.click(within(edit).getByRole('radio', { name: 'Protected' }));
    fireEvent.click(
      within(edit).getByRole('checkbox', { name: 'Share links' }),
    );
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(
      within(edit).getByRole('group', {
        name: 'Ways in',
        description: /Choose at least one way in./,
      }),
    ).toBeTruthy();
    fireEvent.click(within(edit).getByRole('checkbox', { name: 'Password' }));
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(within(edit).getByRole('alert').textContent).toBe(
      'Enter a password of at least 8 characters.',
    );
    expect(puts()).toHaveLength(0);
  });

  it('manages the email allowlist', async () => {
    const { api, puts } = fakeServer([
      policy('/', { visibility: 'protected', methods: ['links'] }),
    ]);
    const list = await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    fireEvent.click(within(edit).getByRole('checkbox', { name: 'Email code' }));
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(within(edit).getByRole('alert').textContent).toBe(
      'Email codes need at least one address or domain.',
    );
    fireEvent.change(within(edit).getByRole('textbox', { name: 'Allowlist' }), {
      target: { value: 'ana@example.test\n*.example.org' },
    });
    // GitHub organisations belong to sign-in only.
    expect(
      within(edit).queryByRole('textbox', { name: 'GitHub organisations' }),
    ).toBeNull();
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    await screen.findByText(/Saved the site default/);
    expect(puts()[0]!.body).toEqual({
      path: '/',
      visibility: 'protected',
      methods: ['links', 'email'],
      allowlist: { emails: ['ana@example.test'], domains: ['*.example.org'] },
    });
    expect(within(list).getAllByRole('listitem')[0]!.textContent).toContain(
      'Allowlist: ana@example.test and *.example.org.',
    );
  });

  it('puts GitHub organisation errors beside the organisations', async () => {
    let reject = true;
    const { api, puts } = fakeServer(
      [
        policy('/', {
          visibility: 'protected',
          methods: ['oauth'],
          allowlist: { emails: [], domains: ['example.org'], githubOrgs: [] },
        }),
      ],
      {
        // The server's real answer: the target is the whole allowlist.
        'PUT /api/v0/sites/docs/policies': () =>
          reject
            ? Response.json(
                {
                  code: 'invalid_policy',
                  message:
                    '"org-x" is not a GitHub organisation name like example-org.',
                  target: 'allowlist',
                },
                { status: 400 },
              )
            : Response.json(
                {
                  code: 'invalid_policy',
                  message:
                    '"a@b" is not an email address like name@example.com.',
                  target: 'allowlist',
                },
                { status: 400 },
              ),
      },
    );
    await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    const orgs = within(edit).getByRole('textbox', {
      name: 'GitHub organisations',
    });

    // Caught before sending when it breaks GitHub's naming rules.
    fireEvent.change(orgs, { target: { value: 'good-org\nbad--org' } });
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(
      within(edit).getByRole('textbox', {
        name: 'GitHub organisations',
        description: /“bad--org” is not a GitHub organisation name/,
      }),
    ).toBeTruthy();
    expect(puts()).toHaveLength(0);

    // The server's refusal of an organisation goes beside the organisations.
    fireEvent.change(orgs, { target: { value: 'org-x' } });
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(
      await within(edit).findByRole('textbox', {
        name: 'GitHub organisations',
        description: /"org-x" is not a GitHub organisation name/,
      }),
    ).toBeTruthy();
    expect(
      within(edit)
        .getByRole('textbox', { name: 'Allowlist' })
        .hasAttribute('aria-invalid'),
    ).toBe(false);

    // Any other allowlist refusal stays beside the allowlist.
    reject = false;
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(
      await within(edit).findByRole('textbox', {
        name: 'Allowlist',
        description: /is not an email address/,
      }),
    ).toBeTruthy();
  });

  it('enables sign-in with GitHub organisations', async () => {
    const { api, puts } = fakeServer([
      policy('/', {
        visibility: 'protected',
        methods: ['email'],
        allowlist: { emails: [], domains: ['example.org'], githubOrgs: [] },
      }),
    ]);
    await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    expect(
      (
        within(edit).getByRole('textbox', {
          name: 'Allowlist',
        }) as HTMLTextAreaElement
      ).value,
    ).toBe('example.org');
    fireEvent.click(
      within(edit).getByRole('checkbox', {
        name: 'Sign in with GitHub, Google or single sign-on',
      }),
    );
    fireEvent.change(
      within(edit).getByRole('textbox', { name: 'GitHub organisations' }),
      { target: { value: 'example-org' } },
    );
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    await screen.findByText(/Saved the site default/);
    expect(puts()[0]!.body).toEqual({
      path: '/',
      visibility: 'protected',
      methods: ['email', 'oauth'],
      allowlist: {
        emails: [],
        domains: ['example.org'],
        githubOrgs: ['example-org'],
      },
    });
  });

  it('shows the server’s reason next to the field it names', async () => {
    const { api } = fakeServer([policy('/')], {
      'PUT /api/v0/sites/docs/policies': () =>
        Response.json(
          {
            code: 'invalid_policy',
            message:
              'No OAuth or OIDC providers are configured on this server.',
            target: 'methods',
          },
          { status: 400 },
        ),
    });
    await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    fireEvent.click(within(edit).getByRole('radio', { name: 'Protected' }));
    fireEvent.click(
      within(edit).getByRole('checkbox', {
        name: 'Sign in with GitHub, Google or single sign-on',
      }),
    );
    fireEvent.change(within(edit).getByRole('textbox', { name: 'Allowlist' }), {
      target: { value: 'example.org' },
    });
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect(
      await within(edit).findByRole('group', {
        name: 'Ways in',
        description: /No OAuth or OIDC providers are configured/,
      }),
    ).toBeTruthy();
  });

  it('adds a path policy and refuses a folder that already has one', async () => {
    const { api, puts } = fakeServer([
      policy('/', { visibility: 'protected', methods: ['links'] }),
      policy('/assets/'),
    ]);
    const list = await card(api);
    fireEvent.click(screen.getByRole('button', { name: 'Add path policy' }));
    const add = dialog('Add a path policy to docs');
    const folder = within(add).getByRole('textbox', { name: 'Folder' });
    fireEvent.change(folder, { target: { value: '/docs' } });
    fireEvent.click(within(add).getByRole('button', { name: 'Add policy' }));
    expect(within(add).getByRole('alert').textContent).toContain(
      'starts and ends with a slash',
    );
    fireEvent.change(folder, { target: { value: '/assets/' } });
    fireEvent.click(within(add).getByRole('button', { name: 'Add policy' }));
    expect(within(add).getByRole('alert').textContent).toContain(
      '/assets/ already has a policy',
    );
    fireEvent.change(folder, { target: { value: '/docs/' } });
    fireEvent.click(within(add).getByRole('radio', { name: 'Public' }));
    fireEvent.click(within(add).getByRole('button', { name: 'Add policy' }));
    await screen.findByText('Saved the policy for /docs/.');
    expect(puts()[0]!.body).toEqual({
      path: '/docs/',
      visibility: 'public',
      methods: [],
    });
    expect(
      within(list)
        .getAllByRole('listitem')
        .map((row) => row.querySelector('code')!.textContent),
    ).toEqual(['/', '/assets/', '/docs/']);
  });

  it('deletes a path policy after naming what its paths fall back to', async () => {
    const { api, calls } = fakeServer([
      policy('/', { visibility: 'protected', methods: ['links'] }),
      policy('/docs/'),
    ]);
    const list = await card(api);
    fireEvent.click(
      screen.getByRole('button', { name: 'Delete the policy for /docs/' }),
    );
    const confirm = dialog('Delete the policy for /docs/?');
    expect(confirm.textContent).toContain(
      'will follow the site default instead, so it becomes protected',
    );
    fireEvent.click(
      within(confirm).getByRole('button', { name: 'Delete policy' }),
    );
    await screen.findByText('Deleted the policy for /docs/.');
    expect(calls.some((c) => c.route.startsWith('DELETE'))).toBe(true);
    expect(within(list).getAllByRole('listitem')).toHaveLength(1);
  });

  it('offers to try again when the policies cannot be loaded', async () => {
    let fail = true;
    const { api } = fakeServer([policy('/')], {
      'GET /api/v0/sites/docs/policies': () =>
        fail
          ? Response.json(
              { code: 'internal', message: 'Something went wrong.' },
              { status: 500 },
            )
          : Response.json({ items: [policy('/')] }),
    });
    render(<PoliciesCard api={api} site={docs} />);
    await screen.findByText(/Could not load the access policies/);
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(
      await screen.findByRole('list', { name: 'Access policies' }),
    ).toBeTruthy();
  });
});

describe('on the site page', () => {
  it('sits on the Access tab and updates the header’s access word', async () => {
    window.history.replaceState(null, '', '/sites/docs/access');
    const { api } = fakeServer([policy('/')]);
    render(<App api={api} />);
    const policies = await screen.findByRole('region', {
      name: 'Access policies',
    });
    expect(
      screen.getByRole('link', { name: 'Access' }).getAttribute('aria-current'),
    ).toBe('page');
    await waitFor(() =>
      expect(document.title).toBe('Access · docs · Wardlock'),
    );
    const word = () =>
      screen.getByRole('heading', { level: 1 }).parentElement!
        .previousElementSibling!.textContent;
    expect(word()).toBe('Public');

    await within(policies).findByRole('list', { name: 'Access policies' });
    fireEvent.click(
      within(policies).getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    fireEvent.click(within(edit).getByRole('radio', { name: 'Protected' }));
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    await waitFor(() => expect(word()).toBe('Protected'));
  });

  it('says a saved change could not refresh the site details, not the deploys', async () => {
    window.history.replaceState(null, '', '/sites/docs/access');
    let siteReads = 0;
    const { api } = fakeServer([policy('/')], {
      'GET /api/v0/sites/docs': () =>
        ++siteReads === 1
          ? Response.json(docs)
          : Response.json(
              { code: 'internal', message: 'Something went wrong.' },
              { status: 500 },
            ),
    });
    render(<App api={api} />);
    const policies = await screen.findByRole('region', {
      name: 'Access policies',
    });
    await within(policies).findByRole('list', { name: 'Access policies' });
    fireEvent.click(
      within(policies).getByRole('button', { name: 'Edit the site default' }),
    );
    const edit = dialog('Edit the site default for docs');
    fireEvent.click(within(edit).getByRole('radio', { name: 'Protected' }));
    fireEvent.click(within(edit).getByRole('button', { name: 'Save policy' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      'Saved, but could not refresh the site details. The access shown here may be out of date.',
    );
    expect(
      screen.queryByText(/Could not refresh the deploy history/),
    ).toBeNull();
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeTruthy();
  });
});
