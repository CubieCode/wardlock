// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  EmptyState,
  Notice,
  PageHeader,
  SegmentedControl,
  SelectField,
  StackedBars,
} from '@wardlock/ui';
import { useEffect, useMemo, useState } from 'react';
import { tagSlot } from '../shell/navigator.js';
import { useTitle } from '../shell/page.js';
import { navigate } from '../shell/router.js';
import { Breakdown, type BreakdownRow } from './breakdown.js';
import {
  analyticsHref,
  browserNames,
  loadReport,
  ranges,
  readQuery,
  requestsPerDay,
  viaNames,
  type AnalyticsDays,
  type Report,
  type Scope,
} from './data.js';
import './analytics.css';

const count = new Intl.NumberFormat('en-NZ');
const percent = new Intl.NumberFormat('en-NZ', {
  style: 'percent',
  maximumFractionDigits: 1,
});
const requests = (n: number) =>
  `${count.format(n)} ${n === 1 ? 'request' : 'requests'}`;

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'missing' }
  | { kind: 'ready'; report: Report };

/** The scope's name in a sentence: "all your sites", "the Clients group". */
function scopeText(scope: Scope, report: Report) {
  switch (scope.kind) {
    case 'all':
      return 'all your sites';
    case 'site':
      return scope.name;
    case 'group': {
      const name = report.groups.find((g) => g.id === scope.id)?.name;
      return name ? `the ${name} group` : 'this group';
    }
    case 'tag': {
      const name = report.tags.find((t) => t.id === scope.id)?.name;
      return name ? `sites tagged ${name}` : 'this tag';
    }
  }
}

/** The scope picker's value for a scope. */
const scopeValue = (scope: Scope) =>
  scope.kind === 'group'
    ? `g:${scope.id}`
    : scope.kind === 'tag'
      ? `t:${scope.id}`
      : scope.kind === 'site'
        ? `s:${scope.name}`
        : '';

function parseScope(value: string): Scope {
  const [kind, ...rest] = value.split(':');
  const id = rest.join(':');
  if (kind === 'g') return { kind: 'group', id };
  if (kind === 't') return { kind: 'tag', id };
  if (kind === 's') return { kind: 'site', name: id };
  return { kind: 'all' };
}

/**
 * `/analytics`: requests to all the owner's sites, or to one group, tag or
 * site (`g`, `t`, `site`), over the last 7, 30 or 90 days (`days`).
 */
export function AnalyticsPage({
  api,
  query,
}: {
  api: ApiClient;
  query: URLSearchParams;
}) {
  useTitle('Analytics');
  const { scope, days } = useMemo(() => readQuery(query), [query]);
  // Each scope, range and Try again is its own read; until its answer
  // arrives the page shows it loading, not the last one's numbers.
  const [attempt, setAttempt] = useState(0);
  const key = `${analyticsHref(scope, days)}#${attempt}`;
  const [loaded, setLoaded] = useState<{ key: string; state: State }>();
  const state: State = loaded?.key === key ? loaded.state : { kind: 'loading' };

  useEffect(() => {
    let live = true;
    const settle = (next: State) => live && setLoaded({ key, state: next });
    loadReport(api, scope, days).then(
      (result) =>
        settle(
          result.kind === 'missing'
            ? { kind: 'missing' }
            : { kind: 'ready', report: result.report },
        ),
      () => settle({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, key, scope, days]);

  const ready = state.kind === 'ready' ? state.report : undefined;
  return (
    <div className="analytics">
      <PageHeader
        title="Analytics"
        description={
          ready
            ? `Requests to ${scopeText(scope, ready)} over the last ${days} days.`
            : 'Requests to your sites, allowed and refused.'
        }
      />
      <div className="analytics-tools">
        <SelectField
          label="Sites"
          value={scopeValue(scope)}
          disabled={!ready}
          onChange={(event) =>
            navigate(analyticsHref(parseScope(event.target.value), days))
          }
        >
          <option value="">All sites</option>
          {ready && ready.groups.length > 0 ? (
            <optgroup label="Groups">
              {ready.groups.map((group) => (
                <option key={group.id} value={`g:${group.id}`}>
                  {group.name}
                </option>
              ))}
            </optgroup>
          ) : null}
          {ready && ready.tags.length > 0 ? (
            <optgroup label="Tags">
              {ready.tags.map((tag) => (
                <option key={tag.id} value={`t:${tag.id}`}>
                  {tag.name}
                </option>
              ))}
            </optgroup>
          ) : null}
          {scope.kind === 'site' ? (
            <optgroup label="Site">
              <option value={scopeValue(scope)}>{scope.name}</option>
            </optgroup>
          ) : null}
        </SelectField>
        <SegmentedControl
          label="Time range"
          value={String(days)}
          options={ranges.map((range) => ({
            value: String(range),
            label: `${range} days`,
          }))}
          onChange={(value) =>
            navigate(analyticsHref(scope, Number(value) as AnalyticsDays), {
              replace: true,
            })
          }
        />
      </div>
      {state.kind === 'loading' ? (
        <p role="status" className="analytics-note">
          Counting requests…
        </p>
      ) : null}
      {state.kind === 'error' ? (
        <Notice tone="danger">
          <div className="analytics-error">
            <span>Could not load analytics. Check your connection.</span>
            <Button type="button" onClick={() => setAttempt((n) => n + 1)}>
              Try again
            </Button>
          </div>
        </Notice>
      ) : null}
      {state.kind === 'missing' ? (
        <EmptyState
          title="Nothing to count here"
          action={<a href="/analytics">Show all sites</a>}
        >
          That site, group or tag does not exist, or is not yours.
        </EmptyState>
      ) : null}
      {ready ? <ReportView scope={scope} days={days} report={ready} /> : null}
    </div>
  );
}

function ReportView({
  scope,
  days,
  report,
}: {
  scope: Scope;
  days: AnalyticsDays;
  report: Report;
}) {
  const { analytics, totals } = report;
  const allowed = analytics.total.allowed;
  const refused = analytics.total.refused;
  const all = allowed + refused;

  return (
    <>
      <section aria-label="Totals">
        <dl className="analytics-kpis">
          <div className="analytics-kpi analytics-kpi-hero">
            <dt>Allowed requests</dt>
            <dd>{count.format(allowed)}</dd>
            <dd className="analytics-note">
              {days} days · {scopeText(scope, report)}
            </dd>
          </div>
          <div className="analytics-kpi">
            <dt>Refused requests</dt>
            <dd>{count.format(refused)}</dd>
            <dd className="analytics-note">
              {all > 0
                ? `${percent.format(refused / all)} of requests`
                : 'No requests'}
            </dd>
          </div>
          <div className="analytics-kpi">
            <dt>Working links</dt>
            <dd>{count.format(totals.workingLinks)}</dd>
            <dd className="analytics-note">Share links that work now</dd>
          </div>
          <div className="analytics-kpi">
            <dt>Quiet sites</dt>
            <dd>{count.format(totals.quiet)}</dd>
            <dd className="analytics-note">
              Of {count.format(totals.sites)}, no requests in 30 days
            </dd>
          </div>
        </dl>
      </section>
      {all === 0 ? (
        <EmptyState title="No requests yet">
          Requests to {scopeText(scope, report)} in the last {days} days show
          here as they arrive.
        </EmptyState>
      ) : (
        <Charts scope={scope} days={days} report={report} />
      )}
    </>
  );
}

function Charts({
  scope,
  days,
  report,
}: {
  scope: Scope;
  days: AnalyticsDays;
  report: Report;
}) {
  const { analytics, groups, tags } = report;
  const total = analytics.total.allowed + analytics.total.refused;
  const oneSite = scope.kind === 'site';
  const siteHint = (site: string) => (oneSite ? undefined : site);

  const via: BreakdownRow[] = analytics.via.flatMap((row) =>
    row.via === 'none'
      ? []
      : [{ key: row.via, label: viaNames[row.via], ...row }],
  );
  const byGroup: BreakdownRow[] = analytics.groups.map((row) => {
    const group = groups.find((g) => g.id === row.groupId);
    return {
      key: row.groupId ?? 'none',
      label: row.groupId ? (group?.name ?? 'Removed group') : 'No group',
      href: row.groupId
        ? analyticsHref({ kind: 'group', id: row.groupId }, days)
        : undefined,
      allowed: row.allowed,
      refused: row.refused,
    };
  });
  const byTag: BreakdownRow[] = analytics.tags.map((row) => {
    const tag = tags.find((t) => t.id === row.tagId);
    return {
      key: row.tagId,
      label: tag?.name ?? 'Removed tag',
      href: analyticsHref({ kind: 'tag', id: row.tagId }, days),
      slot: tag ? tagSlot(tag.colour) : 0,
      allowed: row.allowed,
      refused: row.refused,
    };
  });
  const sites: BreakdownRow[] = analytics.sites.map((row) => ({
    key: row.site,
    label: row.site,
    href: analyticsHref({ kind: 'site', name: row.site }, days),
    allowed: row.allowed,
    refused: row.refused,
  }));
  const links: BreakdownRow[] = analytics.links.map((row) => ({
    key: `${row.site}/${row.publicId}`,
    label: row.label ?? `Link ${row.publicId}`,
    hint: siteHint(row.site),
    href: `/sites/${encodeURIComponent(row.site)}/links/${row.publicId}`,
    allowed: row.allowed,
    refused: row.refused,
  }));
  const paths: BreakdownRow[] = analytics.paths.map((row) => ({
    key: `${row.site}${row.path}`,
    label: row.path,
    hint: siteHint(row.site),
    allowed: row.allowed,
    refused: row.refused,
  }));
  const browsers: BreakdownRow[] = analytics.browsers.map((row) => ({
    key: row.browser,
    label: browserNames[row.browser],
    allowed: row.allowed,
    refused: row.refused,
  }));
  const otherPaths =
    analytics.otherPaths.allowed + analytics.otherPaths.refused;

  return (
    <>
      <Card title="Requests per day">
        <p className="analytics-note">
          Allowed and refused, by day in your timezone.
        </p>
        <StackedBars
          label={`Requests per day, last ${days} days`}
          series={['Allowed', 'Refused']}
          points={requestsPerDay(analytics)}
          periodHeader="Day"
        />
      </Card>
      <div className="analytics-grid">
        <Breakdown
          title="How visitors got in"
          description="Allowed requests by the way in that let them through."
          nameHeader="Way in"
          rows={via}
          total={analytics.total.allowed}
          allowedOnly
          empty="No requests were allowed."
          footnote={
            analytics.total.refused > 0
              ? `${requests(analytics.total.refused)} refused, not counted here.`
              : undefined
          }
        />
        {scope.kind === 'all' || scope.kind === 'tag' ? (
          <Breakdown
            title="By group"
            description="Each site counts under the group it is in now. Choose one to see only its sites."
            nameHeader="Group"
            rows={byGroup}
            total={total}
            empty="No grouped sites had requests."
          />
        ) : null}
        {oneSite ? null : (
          <Breakdown
            title="By tag"
            description="A site counts under each of its tags, so these can add up to more than the total."
            nameHeader="Tag"
            rows={byTag}
            empty="No tagged sites had requests."
          />
        )}
        {oneSite ? null : (
          <Breakdown
            title="Top sites"
            description="The busiest sites. Choose one to see only its requests."
            nameHeader="Site"
            rows={sites}
            total={total}
            limit={10}
            empty="No site had requests."
          />
        )}
        <Breakdown
          title="Top share links"
          description="Requests through each link, by label."
          nameHeader="Link"
          hintHeader={oneSite ? undefined : 'Site'}
          rows={links}
          empty="No requests came through share links."
        />
        <Breakdown
          title="Top pages"
          description="The paths with the most requests."
          nameHeader="Path"
          hintHeader={oneSite ? undefined : 'Site'}
          rows={paths}
          total={total}
          empty="No paths had requests."
          footnote={
            otherPaths > 0
              ? `${requests(otherPaths)} for less common paths are not listed.`
              : undefined
          }
        />
        <Breakdown
          title="Browsers and tools"
          description="From each request’s browser or client. Scripts and tools include command-line clients and crawlers."
          nameHeader="Browser or tool"
          rows={browsers}
          total={total}
          empty="No requests."
        />
      </div>
    </>
  );
}
