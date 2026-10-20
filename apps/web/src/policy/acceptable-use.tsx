// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

import { AppShell, Notice } from '@wardlock/ui';
import { Fragment } from 'react';
import source from '../../../../docs/ACCEPTABLE-USE.md?raw';
import { useTitle } from '../shell/page.js';
import { parseMarkdown, type Block, type Inline } from './markdown.js';
import './policy.css';

/** Where the dashboard shows the acceptable use policy. */
export const acceptableUsePath = '/acceptable-use';

// Parsed once, at load: the policy is bundled from docs/ at build time, so
// the page always matches this server's version.
const policy = parseMarkdown(source);

/**
 * The line a new owner sees before their account is created or first used:
 * continuing means agreeing, with no checkbox. The link opens a new tab, so
 * the one-time code held by the page in front of it is not lost.
 */
export function PolicyAgreement() {
  return (
    <p className="policy-agreement">
      By continuing you agree to the{' '}
      <a href={acceptableUsePath} target="_blank" rel="noopener">
        acceptable use policy
      </a>
      .
    </p>
  );
}

/** `/acceptable-use`: the policy, readable signed in or out. */
export function AcceptableUsePage() {
  useTitle('Acceptable use policy');
  return (
    <AppShell productName="Wardlock">
      <article className="policy">
        {policy.map((block, index) => (
          <PolicyBlock key={index} block={block} />
        ))}
      </article>
    </AppShell>
  );
}

function PolicyBlock({ block }: { block: Block }) {
  switch (block.kind) {
    case 'h1':
      return (
        <h1>
          <Text content={block.content} />
        </h1>
      );
    case 'h2':
      return (
        <h2>
          <Text content={block.content} />
        </h2>
      );
    case 'p':
      return (
        <p>
          <Text content={block.content} />
        </p>
      );
    case 'note':
      return (
        <Notice tone="warning">
          <Text content={block.content} />
        </Notice>
      );
    case 'list':
      return (
        <ul>
          {block.items.map((item, index) => (
            <li key={index}>
              <Text content={item} />
            </li>
          ))}
        </ul>
      );
    case 'details':
      return (
        <details className="policy-details">
          <summary>{block.summary}</summary>
          {block.blocks.map((inner, index) => (
            <PolicyBlock key={index} block={inner} />
          ))}
        </details>
      );
  }
}

function Text({ content }: { content: Inline[] }) {
  return content.map((part, index) => (
    <Fragment key={index}>
      {part.kind === 'strong' ? (
        <strong>{part.text}</strong>
      ) : part.kind === 'code' ? (
        <code>{part.text}</code>
      ) : part.kind === 'link' ? (
        <a href={part.href} target="_blank" rel="noopener noreferrer">
          {part.text}
        </a>
      ) : (
        part.text
      )}
    </Fragment>
  ));
}
