// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest';
import {
  checkCliUses,
  checkSkills,
  cliUses,
  helpCommands,
  helpFlags,
  helpSubcommands,
  parseFrontmatter,
  parseUse,
  shellWords,
} from './check-skills.mjs';

/** @param {string} name @param {string} [extra] */
const skill = (name, extra = '') =>
  `---\nname: ${name}\ndescription: Does a thing.\n${extra}---\n\n# ${name}\n`;
const internal = 'metadata:\n  internal: true\n';

describe('frontmatter', () => {
  it('reads scalars and one nested mapping', () => {
    expect(
      parseFrontmatter(
        `---\nname: x\ndescription: "a: b"\nlicense: 'it''s'\n${internal}---\n`,
      ),
    ).toEqual({
      name: 'x',
      description: 'a: b',
      license: "it's",
      metadata: { internal: true },
    });
  });

  it('rejects what YAML rejects', () => {
    expect(() => parseFrontmatter('---\ndescription: a: b\n---\n')).toThrow(
      /quote/,
    );
    expect(() => parseFrontmatter('# no frontmatter\n')).toThrow();
  });
});

describe('skill set', () => {
  const locked = { vendored: {} };

  it('accepts only the public skill without the internal flag', () => {
    expect(
      checkSkills(
        [
          {
            dir: 'skills/wardlock-publish',
            contents: skill('wardlock-publish'),
          },
          { dir: '.agents/skills/mine', contents: skill('mine', internal) },
          { dir: '.agents/skills/vendored', contents: skill('vendored') },
        ],
        locked,
      ),
    ).toEqual([]);
  });

  it('flags missing internal flags, wrong names and stray vendored skills', () => {
    const problems = checkSkills(
      [
        { dir: 'skills/wardlock-publish', contents: skill('wardlock-publish') },
        { dir: '.agents/skills/mine', contents: skill('mine') },
        { dir: '.agents/skills/other', contents: skill('renamed', internal) },
        { dir: 'skills/vendored', contents: skill('vendored') },
      ],
      locked,
    );
    expect(problems).toHaveLength(3);
    expect(problems[0]).toMatch(/mine.*internal/);
    expect(problems[1]).toMatch(/name is renamed/);
    expect(problems[2]).toMatch(/vendored skill belongs/);
  });

  it('needs the public skill, not internal', () => {
    expect(
      checkSkills(
        [
          {
            dir: 'skills/wardlock-publish',
            contents: skill('wardlock-publish', internal),
          },
        ],
        {},
      ),
    ).toEqual([
      'skills/wardlock-publish: wardlock-publish must not be internal',
      'no wardlock-publish skill found',
    ]);
  });
});

describe('CLI uses', () => {
  it('splits shell words', () => {
    expect(shellWords(`a --m "x y" 'z'|b && c`)).toEqual([
      'a',
      '--m',
      'x y',
      'z',
      '|',
      'b',
      '&&',
      'c',
    ]);
  });

  it('finds invocations in code spans and fences', () => {
    const md =
      'Run `wardlock --json version`, not wardlock.json or `go install wardlock.nz/cli`.\n\n```sh\nwardlock share list ROOT | jq .\n```\n';
    expect(cliUses(md).map((u) => u.words)).toEqual([
      ['share', 'list', 'ROOT'],
      ['--json', 'version'],
    ]);
  });

  it('separates the command path from flags', () => {
    const subs = (/** @type {string} */ c) => (c === 'share' ? ['list'] : []);
    expect(
      parseUse(['--server', 'URL', '--json', 'share', 'list', 'R'], subs),
    ).toEqual({
      leading: ['--server', 'URL', '--json'],
      path: ['share', 'list'],
      flags: ['server', 'json'],
    });
    expect(
      parseUse(['deploy', 'DIR', 'R', '--path=/x/', '--message', 'M'], subs),
    ).toEqual({ leading: [], path: ['deploy'], flags: ['path', 'message'] });
  });

  const top =
    'Usage:\n  wardlock <command>\n\nCommands:\n  deploy    Publish\n  share     Links\n  policy    Policy\n\nRun it.\n';
  const helps = {
    '': top,
    deploy:
      'Usage: wardlock deploy [DIR] [DOMAIN] [flags]\n  -json\n    \tx\n  -path string\n    \tx\n',
    share: 'Usage: wardlock share <create|list> DOMAIN\n',
    'share create':
      'Usage: wardlock share create DOMAIN\n  -expires string\n    \tx\n',
    policy: 'Usage: wardlock policy set DOMAIN [--path P]\n',
    'policy set': 'Usage: wardlock policy set DOMAIN\n  -protected\n    \tx\n',
    '--json deploy':
      'Usage: wardlock deploy [DIR] [DOMAIN] [flags]\n  -json\n    \tx\n  -path string\n    \tx\n',
    // A command that refuses a leading global flag prints only its usage.
    '--json policy set': 'Usage: wardlock policy set DOMAIN [--path P]\n',
  };
  const help = (/** @type {string[]} */ args) =>
    /** @type {Record<string, string>} */ (helps)[args.join(' ')] ?? '';

  it('reads commands, subcommands and flags from help', () => {
    expect(helpCommands(top)).toEqual(['deploy', 'share', 'policy']);
    expect(helpSubcommands(helps.share, 'share')).toEqual(['create', 'list']);
    expect(helpSubcommands(helps.policy, 'policy')).toEqual(['set']);
    expect(helpSubcommands(helps.deploy, 'deploy')).toEqual([]);
    expect(helpFlags(helps.deploy)).toEqual(['json', 'path']);
  });

  it('accepts built commands and flags only', () => {
    const doc = (/** @type {string} */ text) => [
      { file: 'SKILL.md', contents: text },
    ];
    expect(
      checkCliUses(
        doc(
          '`wardlock --json deploy D R --path /x/` and `wardlock policy set R --protected` and `wardlock share create R --expires 7d`',
        ),
        help,
      ),
    ).toEqual([]);
    expect(checkCliUses(doc('`wardlock pull R`'), help)[0]).toMatch(
      /no command "pull"/,
    );
    expect(checkCliUses(doc('`wardlock share rotate R X`'), help)[0]).toMatch(
      /no subcommand "rotate"/,
    );
    expect(checkCliUses(doc('`wardlock deploy D R --force`'), help)[0]).toMatch(
      /has no --force/,
    );
    expect(
      checkCliUses(doc('`wardlock --json policy set R --protected`'), help),
    ).toEqual([
      'SKILL.md: `wardlock --json policy set R --protected`: "wardlock --json policy set" has no --json',
      'SKILL.md: `wardlock --json policy set R --protected`: "wardlock --json policy set" has no --protected',
    ]);
  });
});
