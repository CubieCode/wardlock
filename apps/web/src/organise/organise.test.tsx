// @vitest-environment jsdom
// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { createApiClient, type components } from '@wardlock/apiclient-ts';
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OrganisePage } from './organise-page.js';

type SiteCounts = components['schemas']['SiteCounts'];

const clients = '01928c5e-7a1b-7c3d-9e4f-00000000a001';
const archive = '01928c5e-7a1b-7c3d-9e4f-00000000a002';
const docs = '01928c5e-7a1b-7c3d-9e4f-00000000b001';
const preview = '01928c5e-7a1b-7c3d-9e4f-00000000b002';
const created = 1767225600000;

const groups = [
  { id: archive, name: 'Archive', createdAt: created },
  {
    id: clients,
    name: 'Client work',
    note: 'Previews for clients',
    createdAt: created,
  },
];
const tags = [
  { id: docs, name: 'docs', colour: 3, createdAt: created },
  { id: preview, name: 'preview', colour: 1, createdAt: created },
];
const counts: SiteCounts = {
  all: 12,
  views: { pinned: 0, expiring: 0, public: 5, quiet: 0, untagged: 7, down: 0 },
  groups: [
    { groupId: archive, sites: 1 },
    { groupId: clients, sites: 9 },
  ],
  noGroup: 2,
  tags: [
    { tagId: docs, sites: 4 },
    { tagId: preview, sites: 0 },
  ],
};

type Handler = (body: unknown) => Response | Promise<Response>;

/** A fake server: `routes["POST /api/v0/groups"]` answers that call. */
function fakeServer(routes: Record<string, Handler> = {}) {
  const calls: { route: string; body: unknown }[] = [];
  const all: Record<string, Handler> = {
    'GET /api/v0/groups': () => Response.json({ items: groups }),
    'GET /api/v0/tags': () => Response.json({ items: tags }),
    'GET /api/v0/site-counts': () => Response.json(counts),
    ...routes,
  };
  const api = createApiClient({
    baseUrl: 'http://wardlock.test',
    fetch: async (input) => {
      const request = input as Request;
      const route = `${request.method} ${new URL(request.url).pathname}`;
      const text = await request.text();
      const body: unknown = text ? JSON.parse(text) : undefined;
      calls.push({ route, body });
      const handler = all[route];
      if (handler) return handler(body);
      return Response.json(
        { code: 'not_found', message: 'Not found.' },
        { status: 404 },
      );
    },
  });
  return {
    api,
    calls,
    last: (route: string) => calls.findLast((c) => c.route === route),
  };
}

const refuse = (status: number, code: string, message: string) => () =>
  Response.json({ code, message, target: 'name' }, { status });

const list = (name: 'Groups' | 'Tags') =>
  screen.getByRole('list', { name: name });
const row = (name: 'Groups' | 'Tags', text: string) =>
  within(list(name))
    .getAllByRole('listitem')
    .find((item) => item.textContent?.includes(text))!;

beforeEach(() => {
  // jsdom has no <dialog> methods; open and close it like a browser would.
  HTMLDialogElement.prototype.showModal ??= function (this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close ??= function (this: HTMLDialogElement) {
    this.open = false;
  };
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('organise page', () => {
  it('lists groups and tags with counts linking to the filtered sites list', async () => {
    render(<OrganisePage api={fakeServer().api} />);
    expect(
      screen.getByRole('heading', { level: 1, name: 'Groups and tags' }),
    ).toBeTruthy();
    await screen.findByRole('list', { name: 'Groups' });
    expect(document.title).toBe('Groups and tags · Wardlock');

    const links = (name: 'Groups' | 'Tags') =>
      within(list(name))
        .getAllByRole('link')
        .map((link) => [
          link.getAttribute('aria-label'),
          link.getAttribute('href'),
        ]);
    expect(links('Groups')).toEqual([
      ['1 site in Archive', `/sites?g=${archive}`],
      ['9 sites in Client work', `/sites?g=${clients}`],
    ]);
    expect(links('Tags')).toEqual([
      ['4 sites tagged docs', `/sites?t=${docs}`],
      ['0 sites tagged preview', `/sites?t=${preview}`],
    ]);
    expect(row('Groups', 'Client work').textContent).toContain(
      'Previews for clients',
    );
    // A tag's colour is always beside its name, and said in words.
    expect(row('Tags', 'docs').textContent).toMatch(/^docs, Colour 3/);
  });

  it('says when nothing could load and loads again', async () => {
    let fail = true;
    render(
      <OrganisePage
        api={
          fakeServer({
            'GET /api/v0/site-counts': () =>
              fail
                ? Response.json(
                    { code: 'internal', message: 'Oops.' },
                    { status: 500 },
                  )
                : Response.json(counts),
          }).api
        }
      />,
    );
    expect((await screen.findByRole('alert')).textContent).toMatch(
      /Could not load your groups and tags/,
    );
    fail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByRole('list', { name: 'Groups' })).toBeTruthy();
  });

  it('shows empty lists with their New buttons', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            'GET /api/v0/groups': () => Response.json({ items: [] }),
            'GET /api/v0/tags': () => Response.json({ items: [] }),
            'GET /api/v0/site-counts': () =>
              Response.json({ ...counts, groups: [], tags: [] }),
          }).api
        }
      />,
    );
    expect(await screen.findByText(/No groups yet/)).toBeTruthy();
    expect(screen.getByText('No tags yet.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'New group' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'New tag' })).toBeTruthy();
  });
});

describe('groups', () => {
  it('creates a group, keeping the list in name order', async () => {
    const server = fakeServer({
      'POST /api/v0/groups': () =>
        Response.json(
          { id: 'g-new', name: 'Blog', createdAt: created },
          { status: 201 },
        ),
    });
    render(<OrganisePage api={server.api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'New group' }));
    const field = screen.getByRole('textbox', { name: 'New group name' });
    expect(document.activeElement).toBe(field);
    fireEvent.change(field, { target: { value: '  Blog ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create group' }));
    await waitFor(() =>
      expect(within(list('Groups')).getAllByRole('listitem')).toHaveLength(3),
    );
    expect(server.last('POST /api/v0/groups')?.body).toEqual({ name: 'Blog' });
    expect(
      within(list('Groups'))
        .getAllByRole('link')
        .map((link) => link.getAttribute('aria-label')),
    ).toEqual([
      '1 site in Archive',
      '0 sites in Blog',
      '9 sites in Client work',
    ]);
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'New group' }),
    );
  });

  it.each([
    [409, 'group_name_taken', 'You already have a group with this name.'],
    [
      400,
      'invalid_request',
      'A group name must be 1-40 characters with no control characters.',
    ],
    [413, 'limit_exceeded', 'You can have at most 100 groups.'],
  ])(
    'shows the server’s %i under the name field',
    async (status, code, message) => {
      render(
        <OrganisePage
          api={
            fakeServer({ 'POST /api/v0/groups': refuse(status, code, message) })
              .api
          }
        />,
      );
      fireEvent.click(await screen.findByRole('button', { name: 'New group' }));
      const field = screen.getByRole('textbox', { name: 'New group name' });
      fireEvent.change(field, { target: { value: 'Client work' } });
      fireEvent.click(screen.getByRole('button', { name: 'Create group' }));
      expect((await screen.findByRole('alert')).textContent).toContain(message);
      expect(field.getAttribute('aria-invalid')).toBe('true');
      // The name stays for the owner to fix, with focus back on it.
      expect((field as HTMLInputElement).value).toBe('Client work');
      expect(document.activeElement).toBe(field);
    },
  );

  it('says when the server cannot be reached', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            'POST /api/v0/groups': () => {
              throw new TypeError('offline');
            },
          }).api
        }
      />,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'New group' }));
    fireEvent.change(screen.getByRole('textbox'), {
      target: { value: 'Blog' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create group' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(
      /Could not reach the server/,
    );
  });

  it('cancels creating with Escape', async () => {
    render(<OrganisePage api={fakeServer().api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'New group' }));
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' });
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'New group' }),
    );
  });

  it('renames a group and shows a refused rename inline', async () => {
    let taken = true;
    const server = fakeServer({
      [`PATCH /api/v0/groups/${clients}`]: (body) =>
        taken
          ? refuse(
              409,
              'group_name_taken',
              'You already have a group with this name.',
            )()
          : Response.json({
              ...groups[1],
              name: (body as { name: string }).name,
            }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Groups' });
    fireEvent.click(screen.getByRole('button', { name: 'Rename Client work' }));
    const field = screen.getByRole('textbox', { name: 'Rename Client work' });
    expect((field as HTMLInputElement).value).toBe('Client work');
    fireEvent.change(field, { target: { value: 'Archive' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect((await screen.findByRole('alert')).textContent).toBe(
      'You already have a group with this name.',
    );

    taken = false;
    fireEvent.change(field, { target: { value: 'Agency' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await screen.findByRole('link', { name: '9 sites in Agency' });
    expect(server.last(`PATCH /api/v0/groups/${clients}`)?.body).toEqual({
      name: 'Agency',
    });
    // Renamed groups move to their place by name.
    expect(
      within(list('Groups'))
        .getAllByRole('link')
        .map((link) => link.getAttribute('aria-label')),
    ).toEqual(['9 sites in Agency', '1 site in Archive']);
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Rename Agency' }),
    );
  });

  /** Opens a delete confirmation and waits for its fresh count. */
  async function confirmDelete(name: string) {
    fireEvent.click(screen.getByRole('button', { name: `Delete ${name}` }));
    const dialog = screen.getByRole('dialog');
    await waitFor(() =>
      expect(within(dialog).queryByText('Counting its sites…')).toBeNull(),
    );
    return dialog;
  }

  it('confirms deleting a group, naming how many sites move to No group', async () => {
    const server = fakeServer({
      [`DELETE /api/v0/groups/${clients}`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Groups' });
    const dialog = await confirmDelete('Client work');
    expect(dialog.getAttribute('aria-labelledby')).toBeTruthy();
    expect(
      screen.getByRole('dialog', { name: 'Delete the group “Client work”?' }),
    ).toBe(dialog);
    expect(dialog.textContent).toContain('Its 9 sites move to No group.');
    expect(dialog.textContent).toContain(
      'Sites are never deleted with a group.',
    );

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(server.last(`DELETE /api/v0/groups/${clients}`)).toBeUndefined();

    await confirmDelete('Client work');
    fireEvent.click(screen.getByRole('button', { name: 'Delete group' }));
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: 'Delete Client work' }),
      ).toBeNull(),
    );
    expect(server.last(`DELETE /api/v0/groups/${clients}`)).toBeTruthy();
    expect(within(list('Groups')).getAllByRole('listitem')).toHaveLength(1);
    expect(screen.getByText('Deleted the group “Client work”.')).toBeTruthy();
  });

  it('counts the sites again when the confirmation opens', async () => {
    let groupSites = 9;
    const server = fakeServer({
      'GET /api/v0/site-counts': () =>
        Response.json({
          ...counts,
          groups: [
            { groupId: archive, sites: 1 },
            { groupId: clients, sites: groupSites },
          ],
        }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('link', { name: '9 sites in Client work' });
    // Sites were moved elsewhere since the page loaded.
    groupSites = 11;
    fireEvent.click(screen.getByRole('button', { name: 'Delete Client work' }));
    const dialog = screen.getByRole('dialog');
    expect(dialog.textContent).toContain('Counting its sites…');
    expect(
      within(dialog)
        .getByRole('button', { name: 'Delete group' })
        .hasAttribute('disabled'),
    ).toBe(true);
    await waitFor(() =>
      expect(dialog.textContent).toContain('Its 11 sites move to No group.'),
    );
    expect(
      within(dialog)
        .getByRole('button', { name: 'Delete group' })
        .hasAttribute('disabled'),
    ).toBe(false);
    expect(
      screen.getByRole('link', { name: '11 sites in Client work' }),
    ).toBeTruthy();
  });

  it('says one site, or none, in the delete confirmation', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            'GET /api/v0/site-counts': () =>
              Response.json({
                ...counts,
                groups: [
                  { groupId: archive, sites: 0 },
                  { groupId: clients, sites: 1 },
                ],
              }),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Groups' });
    expect((await confirmDelete('Client work')).textContent).toContain(
      'Its 1 site moves to No group.',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect((await confirmDelete('Archive')).textContent).toContain(
      'It has no sites.',
    );
  });

  it('moves focus to the next row, the previous one, then New group', async () => {
    const server = fakeServer({
      [`DELETE /api/v0/groups/${archive}`]: () =>
        new Response(null, { status: 204 }),
      [`DELETE /api/v0/groups/${clients}`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Groups' });

    await confirmDelete('Archive');
    fireEvent.click(screen.getByRole('button', { name: 'Delete group' }));
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole('button', { name: 'Rename Client work' }),
      ),
    );

    await confirmDelete('Client work');
    fireEvent.click(screen.getByRole('button', { name: 'Delete group' }));
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole('button', { name: 'New group' }),
      ),
    );
  });

  it('focuses the previous row after deleting the last one', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            [`DELETE /api/v0/groups/${clients}`]: () =>
              new Response(null, { status: 204 }),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Groups' });
    await confirmDelete('Client work');
    fireEvent.click(screen.getByRole('button', { name: 'Delete group' }));
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole('button', { name: 'Rename Archive' }),
      ),
    );
  });

  it('treats a group that is already gone as deleted', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            [`DELETE /api/v0/groups/${clients}`]: () =>
              Response.json(
                { code: 'not_found', message: 'Not found.' },
                { status: 404 },
              ),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Groups' });
    await confirmDelete('Client work');
    fireEvent.click(screen.getByRole('button', { name: 'Delete group' }));
    expect(
      await screen.findByText(
        'The group “Client work” had already been deleted.',
      ),
    ).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(within(list('Groups')).getAllByRole('listitem')).toHaveLength(1);
  });

  it('keeps the dialog open with the reason when deleting fails', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            [`DELETE /api/v0/groups/${clients}`]: () =>
              Response.json(
                { code: 'internal', message: 'Oops.' },
                { status: 500 },
              ),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Groups' });
    const dialog = await confirmDelete('Client work');
    fireEvent.click(screen.getByRole('button', { name: 'Delete group' }));
    expect((await within(dialog).findByRole('alert')).textContent).toMatch(
      /Could not delete: Something went wrong on the server/,
    );
    expect(row('Groups', 'Client work')).toBeTruthy();
  });
});

describe('tags', () => {
  it('creates a tag, which takes the colour the server gives it', async () => {
    const server = fakeServer({
      'POST /api/v0/tags': () =>
        Response.json(
          { id: 't-new', name: 'client-a', colour: 2, createdAt: created },
          { status: 201 },
        ),
    });
    render(<OrganisePage api={server.api} />);
    fireEvent.click(await screen.findByRole('button', { name: 'New tag' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'New tag name' }), {
      target: { value: 'client-a' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create tag' }));
    await screen.findByRole('link', { name: '0 sites tagged client-a' });
    expect(server.last('POST /api/v0/tags')?.body).toEqual({
      name: 'client-a',
    });
    expect(
      within(list('Tags'))
        .getAllByRole('listitem')
        .map((item) => item.textContent?.split(',')[0]),
    ).toEqual(['client-a', 'docs', 'preview']);
    expect(row('Tags', 'client-a').textContent).toContain('Colour 2');
  });

  it.each([
    [409, 'tag_name_taken', 'You already have a tag with this name.'],
    [
      400,
      'invalid_request',
      "A tag name must be 1-30 characters of a-z, 0-9 and '-'.",
    ],
  ])(
    'shows the server’s %i under the name field',
    async (status, code, message) => {
      render(
        <OrganisePage
          api={
            fakeServer({ 'POST /api/v0/tags': refuse(status, code, message) })
              .api
          }
        />,
      );
      fireEvent.click(await screen.findByRole('button', { name: 'New tag' }));
      fireEvent.change(screen.getByRole('textbox'), {
        target: { value: 'Docs' },
      });
      fireEvent.click(screen.getByRole('button', { name: 'Create tag' }));
      expect((await screen.findByRole('alert')).textContent).toBe(message);
    },
  );

  it('renames a tag', async () => {
    const server = fakeServer({
      [`PATCH /api/v0/tags/${docs}`]: (body) =>
        Response.json({ ...tags[0], ...(body as object) }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Rename docs' }));
    const field = screen.getByRole('textbox', { name: 'Rename docs' });
    fireEvent.change(field, { target: { value: 'guides' } });
    fireEvent.submit(field);
    await screen.findByRole('link', { name: '4 sites tagged guides' });
    expect(server.last(`PATCH /api/v0/tags/${docs}`)?.body).toEqual({
      name: 'guides',
    });
  });

  it('picks one of the eight colours or neutral as a radio group, each shown with the name', async () => {
    const server = fakeServer({
      [`PATCH /api/v0/tags/${docs}`]: (body) =>
        Response.json({ ...tags[0], ...(body as object) }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Colour of docs' }));
    const group = screen.getByRole('group', { name: 'Colour for docs' });
    const radios = within(group).getAllByRole('radio');
    expect(radios.map((radio) => radio.getAttribute('name'))).toEqual(
      Array(9).fill(`colour-${docs}`),
    );
    expect(
      radios.map((radio) => [
        radio.closest('.wl-choice')?.textContent,
        (radio as HTMLInputElement).checked,
      ]),
    ).toEqual([
      ['Colour 1docs', false],
      ['Colour 2docs', false],
      ['Colour 3docs', true],
      ['Colour 4docs', false],
      ['Colour 5docs', false],
      ['Colour 6docs', false],
      ['Colour 7docs', false],
      ['Colour 8docs', false],
      ['Neutraldocs', false],
    ]);

    fireEvent.click(within(group).getByRole('radio', { name: 'Neutral' }));
    await waitFor(() =>
      expect(row('Tags', 'docs').textContent).toMatch(/^docs, Neutral/),
    );
    expect(server.last(`PATCH /api/v0/tags/${docs}`)?.body).toEqual({
      colour: 0,
    });
    expect(
      (
        within(group).getByRole('radio', {
          name: 'Neutral',
        }) as HTMLInputElement
      ).checked,
    ).toBe(true);
  });

  it('saves the latest colour chosen while another is saving', async () => {
    const answers: ((response: Response) => void)[] = [];
    const server = fakeServer({
      [`PATCH /api/v0/tags/${docs}`]: (body) =>
        new Promise<Response>((resolve) =>
          answers.push(() =>
            resolve(Response.json({ ...tags[0], ...(body as object) })),
          ),
        ),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Colour of docs' }));
    const radio = (name: string) =>
      screen.getByRole('radio', { name }) as HTMLInputElement;
    fireEvent.click(radio('Colour 5'));
    await waitFor(() => expect(answers).toHaveLength(1));
    // Two more choices while the first is on its way: the last one wins.
    fireEvent.click(radio('Colour 6'));
    fireEvent.click(radio('Colour 7'));
    expect(radio('Colour 7').checked).toBe(true);
    answers[0]!(new Response());
    await waitFor(() => expect(answers).toHaveLength(2));
    answers[1]!(new Response());
    await waitFor(() =>
      expect(row('Tags', 'docs').textContent).toMatch(/^docs, Colour 7/),
    );
    expect(
      server.calls
        .filter((call) => call.route === `PATCH /api/v0/tags/${docs}`)
        .map((call) => call.body),
    ).toEqual([{ colour: 5 }, { colour: 7 }]);
    expect(radio('Colour 7').checked).toBe(true);
  });

  it('says when a colour could not be changed', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            [`PATCH /api/v0/tags/${docs}`]: () =>
              Response.json(
                { code: 'internal', message: 'Oops.' },
                { status: 500 },
              ),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Colour of docs' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Colour 5' }));
    expect((await screen.findByRole('alert')).textContent).toMatch(
      /Could not change the colour/,
    );
    expect(row('Tags', 'docs').textContent).toMatch(/^docs, Colour 3/);
    expect(
      (screen.getByRole('radio', { name: 'Colour 3' }) as HTMLInputElement)
        .checked,
    ).toBe(true);
  });

  it('confirms deleting a tag, saying its sites lose it', async () => {
    const server = fakeServer({
      [`DELETE /api/v0/tags/${docs}`]: () =>
        new Response(null, { status: 204 }),
    });
    render(<OrganisePage api={server.api} />);
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Delete docs' }));
    const dialog = screen.getByRole('dialog', {
      name: 'Delete the tag “docs”?',
    });
    await waitFor(() =>
      expect(dialog.textContent).toContain('Its 4 sites lose the tag.'),
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete tag' }));
    await waitFor(() =>
      expect(within(list('Tags')).getAllByRole('listitem')).toHaveLength(1),
    );
    expect(server.last(`DELETE /api/v0/tags/${docs}`)).toBeTruthy();
    expect(document.activeElement).toBe(
      screen.getByRole('button', { name: 'Colour of preview' }),
    );
  });

  it('says a single site loses the tag', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            'GET /api/v0/site-counts': () =>
              Response.json({
                ...counts,
                tags: [
                  { tagId: docs, sites: 1 },
                  { tagId: preview, sites: 0 },
                ],
              }),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Delete docs' }));
    await waitFor(() =>
      expect(screen.getByRole('dialog').textContent).toContain(
        'Its site loses the tag.',
      ),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete preview' }));
    await waitFor(() =>
      expect(screen.getByRole('dialog').textContent).toContain(
        'No sites have it.',
      ),
    );
  });

  it('treats a tag that is already gone as deleted', async () => {
    render(
      <OrganisePage
        api={
          fakeServer({
            [`DELETE /api/v0/tags/${docs}`]: () =>
              Response.json(
                { code: 'not_found', message: 'Not found.' },
                { status: 404 },
              ),
          }).api
        }
      />,
    );
    await screen.findByRole('list', { name: 'Tags' });
    fireEvent.click(screen.getByRole('button', { name: 'Delete docs' }));
    const confirm = screen.getByRole('button', { name: 'Delete tag' });
    await waitFor(() => expect(confirm.hasAttribute('disabled')).toBe(false));
    fireEvent.click(confirm);
    expect(
      await screen.findByText('The tag “docs” had already been deleted.'),
    ).toBeTruthy();
    expect(within(list('Tags')).getAllByRole('listitem')).toHaveLength(1);
  });
});
