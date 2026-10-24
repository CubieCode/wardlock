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
import { useCallback, useEffect, useMemo, useState } from 'react';
import { tagSlot } from '../shell/navigator.js';
import { useTitle } from '../shell/page.js';
import { navigate } from '../shell/router.js';
import { Breakdown, type BreakdownRow } from './breakdown.js';
import { CountriesCard } from './countries-card.js';
import {
  analyticsHref,
  browserNames,
  parseScope,
  ranges,
  readAnalytics,
  readOrganisation,
  readQuery,
  readTotals,
  requestsPerDay,
  scopeValue,
  viaNames,
  type Analytics,
  type AnalyticsDays,
  type Organisation,
  type Scope,
  type SiteTotals,
} from './data.js';
import './analytics.css';

const count = new Intl.NumberFormat('en-NZ');
const percent = new Intl.NumberFormat('en-NZ', {
  style: 'percent',
  maximumFractionDigits: 1,
});
const requests = (n: number) =>
  `${count.format(n)} ${n === 1 ? 'request' : 'requests'}`;

interface Report extends Organisation {
  analytics: Analytics;
  totals: SiteTotals;
}

type Result<T> =
  { kind: 'loading' } | { kind: 'error' } | { kind: 'ready'; value: T };

/**
 * Reads `input` with `read`, again whenever the input or attempt changes.
 * `result` is for the current input only: until its answer arrives it is
 * loading, never an earlier input's answer, however late that comes. `last`
 * keeps the newest answer of any input.
 */
function useRead<T>(
  input: string,
  attempt: number,
  read: (input: string) => Promise<T>,
) {
  const key = `${input}#${attempt}`;
  const [settled, setSettled] = useState<{ key: string; result: Result<T> }>();
  const [last, setLast] = useState<T>();
  useEffect(() => {
    let live = true;
    const settle = (result: Result<T>) =>
      live && setSettled({ key: `${input}#${attempt}`, result });
    read(input).then(
      (value) => {
        settle({ kind: 'ready', value });
        if (live) setLast(value);
      },
      () => settle({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [input, attempt, read]);
  const result: Result<T> =
    settled?.key === key ? settled.result : { kind: 'loading' };
  return { result, last };
}

/** The scope's name in a sentence: "all your sites", "the Clients group". */
function scopeText(scope: Scope, organisation: Organisation | undefined) {
  switch (scope.kind) {
    case 'all':
      return 'all your sites';
    case 'site':
      return scope.name;
    case 'group': {
      const name = organisation?.groups.find((g) => g.id === scope.id)?.name;
      return name ? `the ${name} group` : 'this group';
    }
    case 'tag': {
      const name = organisation?.tags.find((t) => t.id === scope.id)?.name;
      return name ? `sites tagged ${name}` : 'this tag';
    }
  }
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
  const scoped = scopeValue(scope);

  // Three reads: the group and tag names once, the site totals per scope
  // (they do not depend on the range) and the requests per scope and range.
  // Try again repeats only the reads that failed.
  const [tries, setTries] = useState({ names: 0, totals: 0, requests: 0 });
  const names = useRead(
    'names',
    tries.names,
    useCallback(() => readOrganisation(api), [api]),
  );
  const totals = useRead(
    scoped,
    tries.totals,
    useCallback((value) => readTotals(api, parseScope(value)), [api]),
  );
  const counts = useRead(
    `${days}:${scoped}`,
    tries.requests,
    useCallback(
      (value) => {
        const at = value.indexOf(':');
        return readAnalytics(
          api,
          parseScope(value.slice(at + 1)),
          Number(value.slice(0, at)) as AnalyticsDays,
        );
      },
      [api],
    ),
  );
  const organisation =
    names.result.kind === 'ready' ? names.result.value : names.last;

  const failed =
    counts.result.kind === 'error' ||
    totals.result.kind === 'error' ||
    (names.result.kind === 'error' && !organisation);
  const missing =
    counts.result.kind === 'ready' && counts.result.value === 'missing';
  const report: Report | undefined =
    counts.result.kind === 'ready' &&
    counts.result.value !== 'missing' &&
    totals.result.kind === 'ready' &&
    organisation
      ? {
          analytics: counts.result.value,
          totals: totals.result.value,
          ...organisation,
        }
      : undefined;
  const retry = () =>
    setTries((n) => ({
      names: n.names + (names.result.kind === 'error' ? 1 : 0),
      totals: n.totals + (totals.result.kind === 'error' ? 1 : 0),
      requests: n.requests + (counts.result.kind === 'error' ? 1 : 0),
    }));
  // Scope and range changes both add a history entry, so Back undoes either.
  const show = (next: Scope, nextDays: AnalyticsDays) =>
    navigate(analyticsHref(next, nextDays));
  const known =
    scope.kind === 'group'
      ? organisation?.groups.some((g) => g.id === scope.id)
      : scope.kind === 'tag'
        ? organisation?.tags.some((t) => t.id === scope.id)
        : true;

  return (
    <div className="analytics">
      <PageHeader
        title="Analytics"
        description={
          report
            ? `Requests to ${scopeText(scope, organisation)} over the last ${days} days.`
            : 'Requests to your sites, allowed and refused.'
        }
      />
      <div className="analytics-tools">
        <SelectField
          label="Showing"
          value={scoped}
          onChange={(event) => show(parseScope(event.target.value), days)}
        >
          <option value="">All sites</option>
          {organisation && organisation.groups.length > 0 ? (
            <optgroup label="Groups">
              {organisation.groups.map((group) => (
                <option key={group.id} value={`g:${group.id}`}>
                  {group.name}
                </option>
              ))}
            </optgroup>
          ) : null}
          {organisation && organisation.tags.length > 0 ? (
            <optgroup label="Tags">
              {organisation.tags.map((tag) => (
                <option key={tag.id} value={`t:${tag.id}`}>
                  {tag.name}
                </option>
              ))}
            </optgroup>
          ) : null}
          {scope.kind === 'site' ? (
            <optgroup label="Site">
              <option value={scoped}>{scope.name}</option>
            </optgroup>
          ) : null}
          {known ? null : (
            // A group or tag not (yet) among the names, so the picker still
            // shows what the page is scoped to.
            <option value={scoped}>
              {organisation
                ? scope.kind === 'group'
                  ? 'Unknown group'
                  : 'Unknown tag'
                : 'Loading…'}
            </option>
          )}
        </SelectField>
        <SegmentedControl
          label="Time range"
          value={String(days)}
          options={ranges.map((range) => ({
            value: String(range),
            label: `${range} days`,
          }))}
          onChange={(value) => show(scope, Number(value) as AnalyticsDays)}
        />
      </div>
      {failed ? (
        <Notice tone="danger">
          <div className="analytics-error">
            <span>Could not load analytics.</span>
            <Button type="button" onClick={retry}>
              Try again
            </Button>
          </div>
        </Notice>
      ) : missing ? (
        <EmptyState
          title="Nothing to count here"
          action={
            <a href={analyticsHref({ kind: 'all' }, days)}>Show all sites</a>
          }
        >
          That site, group or tag does not exist, or is not yours.
        </EmptyState>
      ) : report ? (
        <ReportView scope={scope} days={days} report={report} />
      ) : (
        <p role="status" className="analytics-note">
          Counting requests…
        </p>
      )}
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
          <div className="analytics-kpi">
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
            <dd className="analytics-note">No requests in 30 days</dd>
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
      {analytics.countryLookup && analytics.countries ? (
        <CountriesCard
          countries={analytics.countries}
          lookup={analytics.countryLookup}
          total={total}
        />
      ) : null}
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
