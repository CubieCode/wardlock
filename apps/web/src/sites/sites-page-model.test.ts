// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import { describe, expect, it } from 'vitest';
import type { Group, Tag } from './site-page-organise.js';
import { applyChange, inverse, triState } from './sites-page-organise.js';
import {
  queryHref,
  readQuery,
  searchTokens,
  withoutWord,
} from './sites-page-query.js';
import {
  buildSections,
  shownSites,
  type SiteSummary,
} from './sites-page-sections.js';

const g1 = '01928c5e-7a1b-7c3d-9e4f-00000000a001';
const g2 = '01928c5e-7a1b-7c3d-9e4f-00000000a002';
const t1 = '01928c5e-7a1b-7c3d-9e4f-00000000b001';
const t2 = '01928c5e-7a1b-7c3d-9e4f-00000000b002';

const groups: Group[] = [
  { id: g1, name: 'Client work', note: 'For clients', createdAt: 0 },
  { id: g2, name: 'Docs', createdAt: 0 },
];
const tags: Tag[] = [
  { id: t1, name: 'client', colour: 1, createdAt: 0 },
  { id: t2, name: 'preview', colour: 2, createdAt: 0 },
];

function summary(name: string, extra: Partial<SiteSummary> = {}): SiteSummary {
  return {
    id: `id-${name}`,
    name,
    hostname: `${name}.sites.example.test`,
    url: `https://${name}.sites.example.test/`,
    createdAt: 0,
    unavailable: false,
    tagIds: [],
    pinned: false,
    visibility: 'public',
    methods: [],
    workingLinks: 0,
    expiringLinks: 0,
    requests: [0, 0, 0, 0, 0, 0, 0],
    quiet: true,
    ...extra,
  };
}

describe('list address', () => {
  it('reads known values and falls back to defaults for the rest', () => {
    expect(
      readQuery(
        new URLSearchParams(
          'g=none&v=public&by=tag&sort=links&d=compact&q=is:down',
        ),
      ),
    ).toEqual({
      g: 'none',
      t: undefined,
      v: 'public',
      q: 'is:down',
      by: 'tag',
      sort: 'links',
      d: 'compact',
    });
    expect(
      readQuery(new URLSearchParams('v=everything&by=colour&sort=size&d=tiny')),
    ).toEqual({
      g: undefined,
      t: undefined,
      v: undefined,
      q: '',
      by: 'group',
      sort: 'deploy',
      d: 'comfortable',
    });
  });

  it('writes only what differs from the defaults', () => {
    const base = readQuery(new URLSearchParams());
    expect(queryHref(base)).toBe('/sites');
    expect(queryHref({ ...base, q: '   ' })).toBe('/sites');
    expect(
      queryHref({
        ...base,
        t: t1,
        q: 'tag:client docs',
        sort: 'name',
        d: 'compact',
      }),
    ).toBe(`/sites?t=${t1}&q=tag%3Aclient+docs&sort=name&d=compact`);
  });

  it('finds tokens and leaves free text and bare prefixes alone', () => {
    expect(
      searchTokens(
        'docs tag:client group:client-w group:none is:public is:Down is:odd tag:',
      ),
    ).toEqual([
      { word: 'tag:client', label: 'Tag: client' },
      { word: 'group:client-w', label: 'Group: client-w' },
      { word: 'group:none', label: 'No group' },
      { word: 'is:public', label: 'Public' },
      { word: 'is:Down', label: 'Taken down' },
      { word: 'is:odd', label: 'Is: odd' },
    ]);
    expect(withoutWord('docs  tag:client is:public', 'tag:client')).toBe(
      'docs is:public',
    );
    // Any white space separates words, as on the server.
    expect(
      searchTokens('docs\ttag:client\nis:pinned').map((t) => t.word),
    ).toEqual(['tag:client', 'is:pinned']);
  });
});

const view = { by: 'group', sort: 'deploy', d: 'comfortable' } as const;

describe('sections', () => {
  const sites = [
    summary('alpha', { groupId: g2, tagIds: [t1, t2] }),
    summary('beta', { groupId: g1, visibility: 'protected', tagIds: [t1] }),
    summary('gamma', { unavailable: true }),
    summary('delta', { groupId: 'gone' }),
  ];

  it('groups by group in group order, then No group, leaving out empty ones', () => {
    const sections = buildSections(
      sites,
      'group',
      groups,
      tags,
      'All sites',
      view,
    );
    expect(sections.map((s) => [s.title, s.sites.map((x) => x.name)])).toEqual([
      ['Client work', ['beta']],
      ['Docs', ['alpha']],
      // A group that is not listed any more counts as no group.
      ['No group', ['gamma', 'delta']],
    ]);
    expect(sections[0]!.note).toBe('For clients');
    expect(sections[0]!.href).toBe(`/sites?g=${g1}`);
    expect(sections[2]!.href).toBe('/sites?g=none');
  });

  it('keeps how the list is drawn in its links', () => {
    const [first] = buildSections(sites, 'tag', groups, tags, 'All sites', {
      by: 'tag',
      sort: 'name',
      d: 'compact',
    });
    expect(first!.href).toBe(`/sites?t=${t1}&by=tag&sort=name&d=compact`);
  });

  it('puts a site under each of its tags, then Untagged', () => {
    const sections = buildSections(
      sites,
      'tag',
      groups,
      tags,
      'All sites',
      view,
    );
    expect(sections.map((s) => [s.title, s.sites.map((x) => x.name)])).toEqual([
      ['client', ['alpha', 'beta']],
      ['preview', ['alpha']],
      ['Untagged', ['gamma', 'delta']],
    ]);
    expect(sections[0]!.slot).toBe(1);
  });

  it('groups by access with taken down on its own', () => {
    const sections = buildSections(
      sites,
      'access',
      groups,
      tags,
      'All sites',
      view,
    );
    expect(sections.map((s) => [s.title, s.sites.map((x) => x.name)])).toEqual([
      ['Protected', ['beta']],
      ['Public', ['alpha', 'delta']],
      ['Taken down', ['gamma']],
    ]);
  });

  it('keeps the server order in one section when grouping by nothing', () => {
    const [section, ...rest] = buildSections(
      sites,
      'none',
      groups,
      tags,
      'Pinned',
      view,
    );
    expect(rest).toEqual([]);
    expect(section!.title).toBe('Pinned');
    expect(section!.sites.map((s) => s.name)).toEqual([
      'alpha',
      'beta',
      'gamma',
      'delta',
    ]);
  });

  it('shows the first rows of each section only when there are several', () => {
    const section = {
      key: 'x',
      title: 'X',
      sites: Array.from({ length: 7 }, (_, i) => summary(`s${i}`)),
    };
    expect(shownSites(section, 2, 5, false)).toHaveLength(5);
    expect(shownSites(section, 2, 3, false)).toHaveLength(3);
    expect(shownSites(section, 2, 5, true)).toHaveLength(7);
    expect(shownSites(section, 1, 5, false)).toHaveLength(7);
    expect(
      shownSites({ ...section, sites: section.sites.slice(0, 5) }, 2, 5, false),
    ).toHaveLength(5);
  });
});

describe('undo', () => {
  const prior = [
    { site: 'a', tagIds: [t1], pinned: true, groupId: g1 },
    { site: 'b', tagIds: [], pinned: false },
    { site: 'c', tagIds: [t1, t2], pinned: false, groupId: g2 },
  ];

  it('removes an added tag only from the sites that lacked it', () => {
    expect(
      inverse({ sites: ['a', 'b', 'c'], addTagIds: [t1, t2] }, prior),
    ).toEqual([
      { removeTagIds: [t2], sites: ['a'] },
      { removeTagIds: [t1, t2], sites: ['b'] },
    ]);
  });

  it('gives a removed tag back only to the sites that had it', () => {
    expect(
      inverse({ sites: ['a', 'b', 'c'], removeTagIds: [t1] }, prior),
    ).toEqual([{ addTagIds: [t1], sites: ['a', 'c'] }]);
  });

  it('puts each site back in its own group, or none', () => {
    expect(inverse({ sites: ['a', 'b', 'c'], groupId: g1 }, prior)).toEqual([
      { ungroup: true, sites: ['b'] },
      { groupId: g2, sites: ['c'] },
    ]);
    expect(
      inverse({ sites: ['a', 'b'], ungroup: true }, prior.slice(0, 2)),
    ).toEqual([{ groupId: g1, sites: ['a'] }]);
  });

  it('pins back only what the change pinned or unpinned', () => {
    expect(inverse({ sites: ['a', 'b', 'c'], pinned: true }, prior)).toEqual([
      { pinned: false, sites: ['b', 'c'] },
    ]);
    expect(inverse({ sites: ['b'], pinned: false }, prior.slice(1, 2))).toEqual(
      [],
    );
  });

  it('re-pins each unpinned site at its own time, keeping the pin order', () => {
    const pins = [
      { site: 'a', tagIds: [], pinned: true, pinnedAt: 1000 },
      { site: 'b', tagIds: [], pinned: true, pinnedAt: 2000 },
      { site: 'c', tagIds: [], pinned: true, pinnedAt: 1000 },
      { site: 'd', tagIds: [], pinned: false },
    ];
    expect(
      inverse({ sites: ['a', 'b', 'c', 'd'], pinned: false }, pins),
    ).toEqual([
      { pinned: true, pinnedAt: 1000, sites: ['a', 'c'] },
      { pinned: true, pinnedAt: 2000, sites: ['b'] },
    ]);
  });

  it('shows a change on the summaries before the list is read again', () => {
    const order = (ids: string[]) => ids.toSorted();
    const [a, b] = applyChange(
      [summary('a', { tagIds: [t2], groupId: g1 }), summary('b')],
      { sites: ['a'], addTagIds: [t1], ungroup: true, pinned: true },
      order,
    );
    expect(a).toMatchObject({ tagIds: [t1, t2], pinned: true });
    expect(a!.groupId).toBeUndefined();
    expect(b).toEqual(summary('b'));
  });

  it('says whether all, some or none have something', () => {
    expect(triState([1, 2], (n) => n > 0)).toBe(true);
    expect(triState([1, 2], (n) => n > 1)).toBe('mixed');
    expect(triState([1, 2], (n) => n > 2)).toBe(false);
  });
});
