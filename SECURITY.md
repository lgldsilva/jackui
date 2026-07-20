# Security Policy

## Supported versions

Only the latest release is supported with security fixes.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Use the repository's **private vulnerability reporting** ("Report a vulnerability" under the Security tab on GitHub) so the report stays private until a fix is available. Include:

- a description of the issue and its impact;
- steps to reproduce (a proof of concept helps);
- the version/commit you tested.

You should get an initial response within a week. Please allow a reasonable window for a fix before public disclosure.

## Scope notes

- JackUI is **not hardened for direct public-internet exposure**. The supported deployment is behind a reverse proxy on a trusted network, ideally with auth enabled (`JACKUI_AUTH_ENABLED=1`).
- Reports about torrent/media content itself are out of scope — JackUI is a neutral tool; what you access with it is your responsibility (see the Legal section in the README).


