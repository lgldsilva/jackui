<!--
Title: use Conventional Commits, e.g. "fix(streamer): ..." / "feat(web): ..."
-->

## What changes

<!-- What this PR does and why. Link issues: "Closes #123" / "Refs #123". -->

## How it was tested

<!-- Commands run and their result. E.g. `make test`, `cd web && npm test`,
     manual E2E (build/curl/logs). The suite must stay green. -->

## Checklist

- [ ] Conventional Commits in the title
- [ ] `make test` green (Go) and `cd web && npm test` green (if frontend touched)
- [ ] New UI strings use `t()` with keys in **both** `web/src/locales/pt.json` and `en.json`
- [ ] No secrets / internal hosts hardcoded (use `.env` / CI vars/secrets)
- [ ] No new god-files; new logic in its own file/component
