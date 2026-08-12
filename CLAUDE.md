# test-go

Reference test framework for Go services. Public, MIT, and written to be read —
the README is the deliverable, and it is pasted into OneNote as a development
reference. Treat the code as documentation that happens to compile.

## Commits go direct to `main`

**This repo is a sanctioned exception to the trunk-based PR rule in
`~/.claude/CLAUDE.md`.** Commit straight to `main`; no branch, no PR, no review
gate. The opt-out marker `.claude/allow-commit-on-main` at the repo root is what
tells the `block-commit-on-main` hook to allow it.

The reasoning: this is a single-author reference repo with no runtime, no
deploy, and no consumers to break. A PR gate here buys nothing and costs the
thing the repo is for — writing an example down while it is fresh.

That exemption is **not inherited**. It covers this repo only, at this root.

## GitHub Actions is disabled here

Actions is switched off at the repository level, so **nothing runs on push**.
`make ci` is the only gate, and it runs on your machine or not at all.

The workflow in `.github/workflows/test.yml` is kept on purpose: it is part of
what this repo teaches, and it has been verified by executing it in a real
runner container with `act`. Do not delete it, and do not assume it ran.

Re-enable with:

```bash
gh api -X PUT repos/nehsa-net/<repo>/actions/permissions -F enabled=true
```

## What must stay true

- **The suite is green, on every tier, at every commit.** A reference framework
  whose own tests fail teaches the wrong thing more effectively than any README
  teaches the right one. Run `make ci` before committing.
- **Never commit a tier you have not run.** "It compiles" is not "it passes",
  and the integration tier needs Docker running.
- **Keep the README's example output honest.** It quotes real run output,
  including timings and coverage percentages. When behaviour changes, re-run and
  paste the new output rather than editing the numbers by hand.
- **Every example carries its "why".** A snippet showing `t.Cleanup` is worth
  little; a snippet explaining that it runs even after `t.Fatal` is the reason
  somebody reads this repo instead of the Go docs.
- **No internal detail leaks.** No Linear ids, no branch names, no internal
  hostnames in anything a stranger reads — this repo is public. Keep the
  reasoning in comments; keep tracker references out entirely.

## Layout

```
cmd/weatherd/         the service under test — main() only wires
internal/weather/     domain, upstream client, service   (unit tier)
internal/httpapi/     gin router                          (unit tier)
internal/store/       postgres persistence                (integration tier)
internal/config/      env parsing                         (unit tier)
internal/testkit/     helpers shared by the slow tiers
test/integration/     //go:build integration
test/e2e/             //go:build e2e
```

## Commands

`make help` lists everything. The ones that matter: `make test` (unit, no setup),
`make test-integration` (needs Docker), `make test-e2e`, `make ci` (all of it).

## Toolchain note

Go is not on the default PATH on this machine; it is installed at
`~/.local/go/bin`. Either export it or use the full path.
