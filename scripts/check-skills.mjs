// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
//
// Agent skill checks (O4.15):
// - every SKILL.md has a parseable frontmatter whose `name` matches its
//   directory and which has a `description`;
// - the only skill offered to `npx skills add` is wardlock-publish: every
//   other skill sets `metadata.internal: true`, except vendored skills
//   recorded in skills-lock.json and kept in .agents/skills/: the skills CLI
//   never offers a skill the lock records from a project's agent
//   directories, and editing them would be lost on `npx skills update`;
// - every `wardlock` command and flag the public skill mentions is a built
//   command in the CLI's --help.
// Run after `pnpm build`, which puts the CLI in .local/bin.
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { basename, dirname, join } from 'node:path';

/** The one skill published for others to install. */
export const publicSkill = 'wardlock-publish';

/**
 * Parses the YAML frontmatter subset skills use: `key: scalar` lines and
 * one level of nested mappings. Scalars are plain, "double" or 'single'
 * quoted; plain scalars may not contain ": " (YAML rejects them).
 * @param {string} contents
 * @returns {Record<string, unknown>}
 */
export function parseFrontmatter(contents) {
  const match = /^---\r?\n([\s\S]*?)\r?\n---(\r?\n|$)/.exec(contents);
  if (!match) throw new Error('no frontmatter');
  /** @type {Record<string, unknown>} */
  const data = {};
  /** @type {Record<string, unknown> | undefined} */
  let nested;
  for (const [i, line] of (match[1] ?? '').split(/\r?\n/).entries()) {
    if (!line.trim() || line.trimStart().startsWith('#')) continue;
    const m = /^( *)([A-Za-z][\w-]*):(?: (.*))?$/.exec(line);
    if (!m) throw new Error(`line ${i + 2}: not "key: value"`);
    const [, indent, key = '', raw = ''] = m;
    if (indent && !nested) throw new Error(`line ${i + 2}: unexpected indent`);
    if (!indent) nested = undefined;
    const target = nested ?? data;
    if (!raw.trim()) {
      if (indent) throw new Error(`line ${i + 2}: nesting too deep`);
      nested = {};
      data[key] = nested;
      continue;
    }
    target[key] = scalar(raw.trim(), i + 2);
  }
  return data;
}

/**
 * @param {string} raw
 * @param {number} line
 */
function scalar(raw, line) {
  if (raw.startsWith('"')) return JSON.parse(raw);
  if (raw.startsWith("'")) {
    if (!raw.endsWith("'") || raw.length < 2)
      throw new Error(`line ${line}: unclosed quote`);
    return raw.slice(1, -1).replaceAll("''", "'");
  }
  if (/: |\s#/.test(raw))
    throw new Error(`line ${line}: quote a value containing ": " or " #"`);
  if (raw === 'true') return true;
  if (raw === 'false') return false;
  return raw;
}

/**
 * Splits shell-like text into words, honouring quotes. Operators such as
 * `|`, `&&` and `;` come back as their own words.
 * @param {string} text
 */
export function shellWords(text) {
  /** @type {string[]} */
  const words = [];
  const re = /"((?:[^"\\]|\\.)*)"|'([^']*)'|(&&|\|\||[|;<>])|([^\s"'|;&<>]+)/g;
  let word = '';
  let inWord = false;
  let last = 0;
  for (const m of text.matchAll(re)) {
    const gap = text.slice(last, m.index);
    if (/\s/.test(gap) && inWord) {
      words.push(word);
      word = '';
      inWord = false;
    }
    last = m.index + m[0].length;
    if (m[3]) {
      if (inWord) words.push(word);
      words.push(m[3]);
      word = '';
      inWord = false;
      continue;
    }
    word += m[1] ?? m[2] ?? m[4];
    inWord = true;
  }
  if (inWord) words.push(word);
  return words;
}

/**
 * Every `wardlock …` invocation in a Markdown document's code spans and
 * fenced blocks.
 * @param {string} markdown
 * @returns {{ text: string, words: string[] }[]}
 */
export function cliUses(markdown) {
  /** @type {string[]} */
  const snippets = [];
  const withoutFences = markdown.replace(
    /```[^\n]*\n([\s\S]*?)```/g,
    (_, body) => {
      snippets.push(...body.split('\n'));
      return '';
    },
  );
  for (const m of withoutFences.matchAll(/`([^`\n]+)`/g))
    snippets.push(m[1] ?? '');
  /** @type {{ text: string, words: string[] }[]} */
  const uses = [];
  for (const snippet of snippets) {
    const words = shellWords(snippet);
    words.forEach((w, i) => {
      if (w !== 'wardlock') return;
      const end = words.findIndex(
        (x, j) => j > i && ['|', '||', '&&', ';', '<', '>'].includes(x),
      );
      uses.push({
        text: snippet,
        words: words.slice(i + 1, end === -1 ? undefined : end),
      });
    });
  }
  return uses;
}

/**
 * Splits an invocation's words into the global flags before the command
 * (`--json`, `--server URL`), the command path and every flag name.
 * @param {string[]} words
 * @param {(command: string) => string[]} subcommandsOf
 */
export function parseUse(words, subcommandsOf) {
  /** @type {string[]} */
  const leading = [];
  /** @type {string[]} */
  const path = [];
  /** @type {string[]} */
  const flags = [];
  for (let i = 0; i < words.length; i++) {
    const w = words[i] ?? '';
    if (w.startsWith('-')) {
      const name = w.replace(/^--?/, '').split('=')[0] ?? '';
      flags.push(name);
      if (!path.length) {
        leading.push(w);
        if (name === 'server' && !w.includes('='))
          leading.push(words[++i] ?? '');
      }
      continue;
    }
    if (path.length === 0) path.push(w);
    else if (path.length === 1 && subcommandsOf(path[0] ?? '').length)
      path.push(w);
  }
  return { leading, path, flags };
}

/** Commands listed in the CLI's top-level help. @param {string} help */
export function helpCommands(help) {
  const section = help.split(/^Commands:\s*$/m)[1] ?? '';
  return [...section.matchAll(/^ {2}([a-z][\w-]*) {2,}/gm)].map(
    (m) => m[1] ?? '',
  );
}

/**
 * Subcommands from a command's usage line: `<a|b>` or a lower-case word.
 * @param {string} help
 * @param {string} command
 */
export function helpSubcommands(help, command) {
  const usage = new RegExp(`^Usage: wardlock ${command} (\\S+)`, 'm').exec(
    help,
  )?.[1];
  if (!usage) return [];
  const alternatives = /^<([a-z|-]+)>$/.exec(usage);
  if (alternatives) return (alternatives[1] ?? '').split('|');
  return /^[a-z][a-z-]*$/.test(usage) ? [usage] : [];
}

/** Flags a command's help defines. @param {string} help */
export function helpFlags(help) {
  return [...help.matchAll(/^ {2}-([a-z][\w-]*)\b/gm)].map((m) => m[1] ?? '');
}

/**
 * Problems with the set of skills.
 * @param {{ dir: string, contents: string }[]} skills
 * @param {Record<string, unknown>} locked skills-lock.json's skills
 */
export function checkSkills(skills, locked) {
  const problems = [];
  const offered = [];
  for (const { dir, contents } of skills) {
    let data;
    try {
      data = parseFrontmatter(contents);
    } catch (err) {
      problems.push(`${dir}/SKILL.md: ${/** @type {Error} */ (err).message}`);
      continue;
    }
    const name = basename(dir);
    if (data.name !== name)
      problems.push(`${dir}/SKILL.md: name is ${data.name}, expected ${name}`);
    if (typeof data.description !== 'string' || !data.description.trim())
      problems.push(`${dir}/SKILL.md: needs a description`);
    const metadata = /** @type {Record<string, unknown> | undefined} */ (
      data.metadata
    );
    const internal = metadata?.internal === true;
    if (Object.hasOwn(locked, name)) {
      if (dir !== `.agents/skills/${name}`)
        problems.push(`${dir}: a vendored skill belongs in .agents/skills/`);
      continue;
    }
    if (name === publicSkill) {
      if (internal)
        problems.push(`${dir}: ${publicSkill} must not be internal`);
      else offered.push(name);
    } else if (!internal) {
      problems.push(`${dir}/SKILL.md: needs "metadata.internal: true"`);
    }
  }
  if (!offered.length) problems.push(`no ${publicSkill} skill found`);
  return problems;
}

/**
 * Problems with the CLI commands and flags a skill's Markdown uses.
 * @param {{ file: string, contents: string }[]} docs
 * @param {(args: string[]) => string} help output of `wardlock ARGS --help`
 */
export function checkCliUses(docs, help) {
  const problems = [];
  const commands = helpCommands(help([]));
  /** @type {Map<string, string[]>} */
  const subs = new Map();
  const subcommandsOf = (/** @type {string} */ c) => {
    if (!commands.includes(c)) return [];
    if (!subs.has(c)) subs.set(c, helpSubcommands(help([c]), c));
    return subs.get(c) ?? [];
  };
  /** @type {Map<string, string[]>} */
  const flagsOf = new Map();
  for (const { file, contents } of docs) {
    for (const use of cliUses(contents)) {
      const { leading, path, flags } = parseUse(use.words, subcommandsOf);
      const [command, sub = ''] = path;
      if (!command) continue; // `wardlock` named on its own.
      const where = `${file}: \`${use.text}\``;
      if (!commands.includes(command)) {
        problems.push(`${where}: no command "${command}"`);
        continue;
      }
      const known = subcommandsOf(command);
      if (known.length && !known.includes(sub)) {
        problems.push(`${where}: no subcommand "${sub}"`);
        continue;
      }
      // Asked with the same leading global flags, so a command that does
      // not accept them there shows no flags and fails the check.
      const args = [...leading, ...path];
      const key = args.join(' ');
      if (!flagsOf.has(key)) flagsOf.set(key, helpFlags(help(args)));
      for (const flag of flags)
        if (!flagsOf.get(key)?.includes(flag))
          problems.push(`${where}: "wardlock ${key}" has no --${flag}`);
    }
  }
  return problems;
}

function main() {
  const cli = process.env.WARDLOCK_CLI ?? '.local/bin/wardlock';
  if (!existsSync(cli)) {
    console.error(`${cli} not found: run pnpm build first`);
    process.exit(1);
  }
  const dirs = execFileSync(
    'git',
    ['ls-files', '--cached', '--others', '--exclude-standard', '-z'],
    { encoding: 'utf8' },
  )
    .split('\0')
    .filter((p) => basename(p) === 'SKILL.md' && existsSync(p))
    .map(dirname);
  const skills = dirs.map((dir) => ({
    dir,
    contents: readFileSync(join(dir, 'SKILL.md'), 'utf8'),
  }));
  const { skills: locked = {} } = JSON.parse(
    readFileSync('skills-lock.json', 'utf8'),
  );
  const problems = checkSkills(skills, locked);

  const publicDir = `skills/${publicSkill}`;
  const docs = execFileSync('git', [
    'ls-files',
    '--cached',
    '--others',
    '--exclude-standard',
    '-z',
    publicDir,
  ])
    .toString()
    .split('\0')
    .filter((p) => p.endsWith('.md') && existsSync(p))
    .map((file) => ({ file, contents: readFileSync(file, 'utf8') }));
  const help = (/** @type {string[]} */ args) => {
    const res = spawnSync(cli, [...args, '--help'], { encoding: 'utf8' });
    return `${res.stdout}${res.stderr}`;
  };
  problems.push(...checkCliUses(docs, help));

  if (problems.length) {
    console.error(problems.join('\n'));
    process.exit(1);
  }
  console.log(
    `Skills OK (${skills.length} skills; only ${publicSkill} is public; its CLI uses match ${cli} --help).`,
  );
}

if (import.meta.url === `file://${process.argv[1]}`) main();
