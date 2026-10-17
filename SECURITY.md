# Security policy

Wardlock is **alpha** software. It has not had an external security review and should not yet protect anything sensitive.

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**. Do not open a public issue, pull request or discussion.

Include what you found, how to reproduce it, the affected version or commit, and the impact you expect. We will acknowledge your report, keep you updated, and credit you in the advisory if you would like.

There is no bug bounty.

## Scope

In scope: the server (`wardlockd`), the dashboard, the CLI, the API clients and the deployment examples in this repository. Especially: share-link tokens, site sessions and cookies, the reserved `/.wardlock/` path, OAuth flows, rate limits and anything that lets one site or owner reach another's content.

Out of scope: content that owners choose to publish, attacks requiring control of the server or its configuration, and denial of service by volume. See [docs/SECURITY-MODEL.md](docs/SECURITY-MODEL.md) for the threat model.

## Supported versions

Only the latest commit on `main` is supported until the first release.
