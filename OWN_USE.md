# Own-use entry point

Date: 2026-11-07

Instruction marker: 7Nov 15:03 t2585u

This public fork is the reusable pre-ship performance check for a network web app.
The check runs only at the end of that app's development, and only with a separate
authorization. This repository stores the contract and an offline checker. It does
not store app scripts, environment profiles, customer data, or secrets.

## Fork this entry point is for

Verified 2026-11-07 from the GitHub repository API, not from a local assumption:

| Fact | Value |
| --- | --- |
| Fork | https://github.com/markie135/k6 |
| Visibility | public |
| Parent | https://github.com/grafana/k6 |
| Default branch | master |
| License | AGPL-3.0 |
| Enablement base | `4ac4745707bcf432bca392f13c4248e9924a04ed` |

The upstream engine, `LICENSE.md`, git history, and `README.md` stay as they are.
k6 already provides load and stress execution. This change does not add another
engine, a commercial runner, or a sample scenario.

## What was added

- This entry point and the docs under `docs/own-use/`.
- `ownuse/preflight`, a stdlib-level wrapper around two existing k6 parsers
  (`HostnameTrie` and `DNSConfig` / `lib.ParseCIDR`).
- `ownuse/preflight/cmd`, whose only modes are `inspect` and `dry-run`.

The checker reads a profile file, rejects production and incomplete profiles, and
can print the existing `k6` arguments a person would use later. It has no `run`
mode. It does not start a process, open a socket, or write a passed result.

## What to read next

- [Configuration contract](docs/own-use/contract.md)
- [Adoption for a network web app](docs/own-use/adoption.md)
- [CLI, container, and reports](docs/own-use/execution-and-reports.md)

## Profiles stay outside this fork

Safe profiles, approved scripts, and result files belong with the app, not in this
repository. `ownuse/preflight/testdata/profile-shape.json` is a field shape with a
reserved `.test` name. Pointing the checker at that in-repo file is blocked.
Nothing in this change is a production profile.

## Offline check

```bash
go test ./ownuse/preflight/
```

That test does not build a container, does not start `k6 run`, and does not send
traffic. There is no GitHub workflow, schedule, or push hook for this checker.

## Explicitly deferred

Runner execution proof is deferred. A later task would have to show a separate
authorized non-production run and the durable artifacts. This entry point does not
provide that proof.

An AgentDesk `start` / `status` / `results` / `cancel` adapter is not built. The
core path remains the k6 CLI and the unchanged container entrypoint. No AgentDesk
source, config, customer data, or secret is in this fork.
