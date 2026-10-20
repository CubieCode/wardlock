// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

/**
 * A reader for the small Markdown subset docs/ACCEPTABLE-USE.md uses, so the
 * dashboard can show the policy without a Markdown dependency: `#` and `##`
 * headings, paragraphs, `>` notes, `-` lists (one line per item), `<details>`
 * blocks with a `<summary>` line, and inline `**strong**`, `` `code` `` and
 * `[links](https://…)`. Anything else is an error, so the policy cannot use
 * a construct the page would show as raw text.
 */

export type Inline =
  | { kind: 'text' | 'strong' | 'code'; text: string }
  | { kind: 'link'; text: string; href: string };

export type Block =
  | { kind: 'h1' | 'h2' | 'p' | 'note'; content: Inline[] }
  | { kind: 'list'; items: Inline[][] }
  | { kind: 'details'; summary: string; blocks: Block[] };

const inlinePattern = /\*\*(.+?)\*\*|`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\)/g;

export function parseInline(text: string): Inline[] {
  const out: Inline[] = [];
  let at = 0;
  for (const match of text.matchAll(inlinePattern)) {
    if (match.index > at)
      out.push({ kind: 'text', text: text.slice(at, match.index) });
    const [, strong, code, linkText, href] = match;
    if (strong !== undefined) out.push({ kind: 'strong', text: strong });
    else if (code !== undefined) out.push({ kind: 'code', text: code });
    else out.push({ kind: 'link', text: linkText!, href: href! });
    at = match.index + match[0].length;
  }
  if (at < text.length) out.push({ kind: 'text', text: text.slice(at) });
  return out;
}

export function parseMarkdown(source: string): Block[] {
  const root: Block[] = [];
  let blocks = root;
  let paragraph: string[] = [];
  const flush = () => {
    if (paragraph.length)
      blocks.push({ kind: 'p', content: parseInline(paragraph.join(' ')) });
    paragraph = [];
  };

  for (const [index, raw] of source.split('\n').entries()) {
    const line = raw.trimEnd();
    const fail = (why: string): never => {
      throw new Error(`line ${index + 1}: ${why}: ${line}`);
    };
    if (line === '') {
      flush();
      continue;
    }
    if (line === '<details>') {
      flush();
      if (blocks !== root) fail('nested <details>');
      const details: Block = { kind: 'details', summary: '', blocks: [] };
      root.push(details);
      blocks = details.blocks;
      continue;
    }
    const summary = /^<summary>(.+)<\/summary>$/.exec(line);
    if (summary) {
      const details = root.at(-1);
      if (blocks === root || details?.kind !== 'details' || blocks.length)
        return fail('<summary> must open a <details> block');
      details.summary = summary[1]!;
      continue;
    }
    if (line === '</details>') {
      flush();
      if (blocks === root) fail('</details> without <details>');
      const details = root.at(-1);
      if (details?.kind === 'details' && !details.summary)
        fail('<details> without <summary>');
      blocks = root;
      continue;
    }
    if (line.startsWith('<')) fail('unsupported HTML');
    const heading = /^(#{1,2}) (.+)$/.exec(line);
    if (heading) {
      flush();
      blocks.push({
        kind: heading[1] === '#' ? 'h1' : 'h2',
        content: parseInline(heading[2]!),
      });
      continue;
    }
    if (line.startsWith('#')) fail('unsupported heading level');
    if (line.startsWith('> ')) {
      flush();
      blocks.push({ kind: 'note', content: parseInline(line.slice(2)) });
      continue;
    }
    if (line.startsWith('- ')) {
      flush();
      const last = blocks.at(-1);
      const item = parseInline(line.slice(2));
      if (last?.kind === 'list') last.items.push(item);
      else blocks.push({ kind: 'list', items: [item] });
      continue;
    }
    if (/^(\s|\d+\.|\||```)/.test(line)) fail('unsupported Markdown');
    paragraph.push(line);
  }
  flush();
  if (blocks !== root) throw new Error('unclosed <details>');
  return root;
}
