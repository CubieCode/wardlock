// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only
import type { ApiClient } from '@wardlock/apiclient-ts';
import {
  Button,
  Card,
  EmptyState,
  Notice,
  PageHeader,
  Sparkline,
  StatusTag,
} from '@wardlock/ui';
import { useEffect, useState, type ReactNode } from 'react';
import { sitesHref } from '../shell/navigator.js';
import {
  clockIcon,
  downIcon,
  globeIcon,
  quietIcon,
  untaggedIcon,
} from '../shell/icons.js';
import { useTitle } from '../shell/page.js';
import { AccessWord, relativeTime, shortId, Time } from '../sites/format.js';
import {
  expiringTotal,
  groupRows,
  loadOverview,
  shownDeploys,
  shownPinned,
  weekRequests,
  type ExpiringLink,
  type OverviewData,
  type SiteSummary,
} from './overview-data.js';
import './overview.css';

type State =
  | { kind: 'loading' }
  | { kind: 'error' }
  | { kind: 'ready'; data: OverviewData };

const number = new Intl.NumberFormat('en-NZ');
/** "1 site", "1,204 sites". */
const plural = (n: number, one: string, many = `${one}s`) =>
  `${number.format(n)} ${n === 1 ? one : many}`;

const siteHref = (name: string) => `/sites/${encodeURIComponent(name)}`;

/**
 * `/`: the signed-in home page. A greeting with the owner's site and group
 * counts and the server's version, then what needs attention, pinned
 * sites, groups and each site's live deploy, newest first.
 */
export function OverviewPage({ api }: { api: ApiClient }) {
  useTitle('Overview');
  const [state, setState] = useState<State>({ kind: 'loading' });
  const [version, setVersion] = useState<string>();

  // Each attempt loads everything again, the version included; Try again
  // starts a new one.
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let live = true;
    api
      .GET('/api/v0/health')
      .then(({ data }) => live && setVersion(data?.version))
      .catch(() => live && setVersion(undefined));
    loadOverview(api).then(
      (data) => live && setState({ kind: 'ready', data }),
      () => live && setState({ kind: 'error' }),
    );
    return () => {
      live = false;
    };
  }, [api, attempt]);
  const retry = () => {
    setState({ kind: 'loading' });
    setAttempt((n) => n + 1);
  };

  return (
    <>
      <PageHeader
        title="Overview"
        description={
          <Greeting
            data={state.kind === 'ready' ? state.data : undefined}
            version={version}
          />
        }
      />
      {state.kind === 'loading' && <p role="status">Loading your sites…</p>}
      {state.kind === 'error' && (
        <div className="overview-error">
          <Notice tone="danger">
            Could not load your sites. Check your connection and that wardlockd
            is running, then try again.
          </Notice>
          <Button type="button" onClick={retry}>
            Try again
          </Button>
        </div>
      )}
      {state.kind === 'ready' && state.data.counts.all === 0 && (
        <EmptyState
          title="No sites yet"
          action={<a href="/sites">Go to Sites</a>}
        >
          <p>
            Deploy a folder with <code>wardlock deploy</code> to create your
            first site; it will be listed on the Sites page. What needs a look,
            your pinned sites and groups and the latest deploys will show here.
          </p>
        </EmptyState>
      )}
      {state.kind === 'ready' && state.data.counts.all > 0 && (
        <div className="overview-grid">
          <div className="overview-column">
            <NeedsAttention data={state.data} />
            <Groups data={state.data} />
          </div>
          <div className="overview-column">
            <Pinned data={state.data} />
            <LiveDeploys sites={state.data.sites} />
          </div>
        </div>
      )}
    </>
  );
}

/** "Kia ora. You have 12 sites in 3 groups. This server runs Wardlock 0.2.0." */
function Greeting({
  data,
  version,
}: {
  data: OverviewData | undefined;
  version: string | undefined;
}) {
  let sites = '';
  if (data) {
    const { all } = data.counts;
    const groups = data.groups.length;
    sites =
      all === 0
        ? ' You have no sites yet.'
        : groups === 0
          ? ` You have ${plural(all, 'site')} and no groups.`
          : ` You have ${plural(all, 'site')} in ${plural(groups, 'group')}.`;
  }
  return (
    <>
      Kia ora.{sites}
      {version ? (
        <>
          {' '}
          This server runs Wardlock <code>{version}</code>.
        </>
      ) : null}
    </>
  );
}

/** One line of Needs attention: what it is, a detail and where to go. */
function AttentionItem({
  icon,
  title,
  detail,
  href,
  action,
  label,
  children,
}: {
  icon: ReactNode;
  title: ReactNode;
  detail: ReactNode;
  href: string;
  action: string;
  /** Names the link for assistive technology when `action` is generic. */
  label: string;
  children?: ReactNode;
}) {
  return (
    <li className="attention-item">
      <span className="attention-icon">{icon}</span>
      <span className="attention-text">
        <strong>{title}</strong>
        <small>{detail}</small>
      </span>
      <a className="overview-action" href={href} aria-label={label}>
        {action}
      </a>
      {children}
    </li>
  );
}

function ExpiringLinkRow({ link }: { link: ExpiringLink }) {
  const name = link.label ?? 'Unlabelled link';
  return (
    <li className="attention-link">
      <span className="attention-text">
        <span>
          <strong>{name}</strong> on{' '}
          <a href={siteHref(link.site)}>{link.site}</a>
        </span>
        <small>
          Expires {relativeTime(link.expiresAt)}
          {link.usesLeft !== undefined &&
            ` · ${plural(link.usesLeft, 'use')} left`}
        </small>
      </span>
      <a
        className="overview-action"
        href={`${siteHref(link.site)}/links/${encodeURIComponent(link.id)}`}
        aria-label={`View link ${name} on ${link.site}`}
      >
        View link
      </a>
    </li>
  );
}

/**
 * Expiring links, quiet, untagged, public and taken-down sites, each left
 * out when it has nothing to report.
 */
export function NeedsAttention({ data }: { data: OverviewData }) {
  const { views } = data.counts;
  const links = expiringTotal(data.sites);
  const items: ReactNode[] = [];
  if (links > 0)
    items.push(
      <AttentionItem
        key="expiring"
        icon={clockIcon}
        title={`${plural(links, 'link')} ${links === 1 ? 'expires' : 'expire'} within 48 hours`}
        detail={`On ${plural(views.expiring, 'site')}. Check whether anyone still needs them.`}
        href={sitesHref({ v: 'expiring' })}
        action="Review"
        label="Review sites with links expiring soon"
      >
        <ul className="attention-links" aria-label="Links expiring soon">
          {data.expiring.map((link) => (
            <ExpiringLinkRow key={link.id} link={link} />
          ))}
        </ul>
      </AttentionItem>,
    );
  if (views.quiet > 0)
    items.push(
      <AttentionItem
        key="quiet"
        icon={quietIcon}
        title={`${plural(views.quiet, 'site')} had no requests in 30 days`}
        detail="Check whether you still need them."
        href={sitesHref({ v: 'quiet' })}
        action="Review"
        label="Review sites with no requests in 30 days"
      />,
    );
  if (views.untagged > 0)
    items.push(
      <AttentionItem
        key="untagged"
        icon={untaggedIcon}
        title={`${plural(views.untagged, 'site')} ${views.untagged === 1 ? 'has' : 'have'} no tags`}
        detail="Tags make sites easier to find and filter."
        href={sitesHref({ v: 'untagged' })}
        action="Review"
        label="Review untagged sites"
      />,
    );
  if (views.public > 0)
    items.push(
      <AttentionItem
        key="public"
        icon={globeIcon}
        title={`${plural(views.public, 'site')} ${views.public === 1 ? 'is' : 'are'} public`}
        detail="Anyone with the address can open them."
        href={sitesHref({ v: 'public' })}
        action="Check"
        label="Check public sites"
      />,
    );
  if (views.down > 0)
    items.push(
      <AttentionItem
        key="down"
        icon={downIcon}
        title={`${plural(views.down, 'site')} taken down`}
        detail="By this server's operator. Visitors see an unavailable page."
        href={sitesHref({ v: 'down' })}
        action="View"
        label="View taken-down sites"
      />,
    );
  return (
    <Card title="Needs attention">
      {items.length > 0 ? (
        <ul className="attention-list">{items}</ul>
      ) : (
        <p className="overview-quiet">Nothing needs attention.</p>
      )}
    </Card>
  );
}

/** Who can open the site, as a word, with Taken down beside it. */
function Access({ site }: { site: SiteSummary }) {
  return (
    <span className="overview-access">
      <AccessWord policy={site} />
      {site.unavailable && <StatusTag tone="warning">Taken down</StatusTag>}
    </span>
  );
}

/** Pinned sites with access, requests over 7 days and a sparkline. */
export function Pinned({ data }: { data: OverviewData }) {
  const pinned = data.sites.filter((site) => site.pinned);
  return (
    <Card title="Pinned">
      {pinned.length === 0 ? (
        <p className="overview-quiet">
          Pin a site with the star on its page to keep it here.
        </p>
      ) : (
        <ul className="overview-list" aria-label="Pinned sites">
          {pinned.slice(0, shownPinned).map((site) => (
            <li key={site.id} className="pinned-row">
              <span className="pinned-main">
                <a className="overview-name" href={siteHref(site.name)}>
                  {site.name}
                </a>
                <Access site={site} />
              </span>
              <span className="pinned-requests">
                {plural(weekRequests(site), 'request')} in 7 days
              </span>
              <Sparkline values={site.requests} />
            </li>
          ))}
        </ul>
      )}
      {pinned.length > shownPinned && (
        <a href={sitesHref({ v: 'pinned' })}>
          All {number.format(pinned.length)} pinned sites
        </a>
      )}
    </Card>
  );
}

/** Each group's sites, requests over 7 days and working links. */
export function Groups({ data }: { data: OverviewData }) {
  const rows = groupRows(data);
  return (
    <Card title="Groups">
      {rows.length === 0 ? (
        <p className="overview-quiet">
          No groups yet. Put a site in a group from its Settings tab.
        </p>
      ) : (
        <ul className="overview-list" aria-label="Groups">
          {rows.map((row) => (
            <li key={row.group?.id ?? 'none'} className="group-row">
              <a
                className="overview-name"
                href={sitesHref({ g: row.group?.id ?? 'none' })}
              >
                {row.group?.name ?? 'No group'}
              </a>
              <span className="group-stats">
                {plural(row.sites, 'site')} · {plural(row.requests, 'request')}{' '}
                in 7 days · {plural(row.links, 'link')}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

/** Each site's live deploy, newest first: not every deploy. */
export function LiveDeploys({ sites }: { sites: SiteSummary[] }) {
  const live = sites
    .flatMap(({ id, name, liveDeploy }) =>
      liveDeploy ? [{ id, name, deploy: liveDeploy }] : [],
    )
    .slice(0, shownDeploys);
  return (
    <Card title="Recent deploys">
      <p className="overview-sub">
        Each site&rsquo;s live deploy, newest first.
      </p>
      {live.length === 0 ? (
        <p className="overview-quiet">Nothing is live yet.</p>
      ) : (
        <ul className="overview-list" aria-label="Live deploys">
          {live.map(({ id, name, deploy }) => (
            <li key={id} className="deploy-feed-row">
              <a className="overview-name" href={siteHref(name)}>
                {name}
              </a>
              <span className="deploy-feed-message">
                {deploy.message ?? 'No message'}
              </span>
              <small>
                <code>{shortId(deploy.id)}</code> · went live{' '}
                <Time at={deploy.activatedAt} />
              </small>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}
