# Security policy

## Supported versions

Only the **latest release** receives security fixes. A fix ships as a new
release; update with the in-app prompt, or re-run the installer:

```bash
curl -fsSL https://pgtower.dev/install | sh
```

## Reporting a vulnerability

Please **do not open a public issue**. Report it privately through GitHub:
**[Report a vulnerability](https://github.com/9LEVEL/pgtower/security/advisories/new)**
(the *Security* tab of this repository). Only you and the maintainers can see the
report.

Include what you can:

- the pgtower version (`pgtower --version`) and the PostgreSQL version;
- the steps to reproduce it;
- what an attacker gains, and what they need first (a database login, access
  to the machine, …).

You will get a first answer **within 7 days**.

## What happens next

1. We confirm the problem with you and agree on its severity.
2. We fix it privately and publish a new release.
3. We publish a GitHub security advisory, requesting a CVE when it applies, and
   credit you unless you prefer otherwise. Example:
   [GHSA-3p24-2hqx-6rvr](https://github.com/9LEVEL/pgtower/security/advisories/GHSA-3p24-2hqx-6rvr).

## Scope

In scope: the pgtower binary, [`install.sh`](install.sh) and the installer
served at <https://pgtower.dev/install>, and the in-app self-updater.

Out of scope: vulnerabilities in PostgreSQL itself, in your terminal emulator or
in a server's own configuration. Report those to their projects, unless pgtower
makes them worse.
