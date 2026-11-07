# Adopting the check for a network web app

This is the generic adoption path. It is not an integration for a named app, and
it adds no production configuration.

Use it when a network web app is at the end of development and someone has a
separate authorization to run a performance check. Do not use it from an app's
deploy, from a push, or from a schedule.

## Responsibilities

The app repository, which is not this fork, owns:

- the approved k6 script, at a versioned ref and sha256
- the non-production environment and its base URL
- the profile JSON that points at that script and environment
- the artifact directory for a later run
- the decision to invoke the printed `k6` command

This fork owns the contract and the offline checker only.

## Steps

1. Keep the script in the app repository. Do not copy it here. Give it a ref such
   as `network-web/perf/approved.js@v1.0.0` and record its sha256.
2. Write a profile that matches [the contract](contract.md). Set `environment.kind`
   to `nonproduction` and `authorization.phase` to `end-of-development`. Put the
   profile outside this fork.
3. From this repository, inspect the profile. This does not call k6:

   ```bash
   go run ./ownuse/preflight/cmd inspect --profile /absolute/path/outside-this-repo.json
   ```

4. If inspect reports `unexecuted` rather than `blocked`, print the plan:

   ```bash
   go run ./ownuse/preflight/cmd dry-run --profile /absolute/path/outside-this-repo.json
   ```

   Exit `0` means the profile can be planned. The JSON status is `unexecuted`.
   Exit `2` means `blocked`. Exit `1` means the command or the JSON was unusable.
   There is no mode that starts a test. `preflight run` is rejected.

5. Stop here unless a separate authorization says to execute. Execution is not
   part of this adoption step. A later operator would run the printed `k6`
   command by hand, against the non-production URL in that profile, and keep the
   artifacts named by `plannedRunID`.

## What the app script is expected to do

The planned command passes the target only as `OWNUSE_TARGET_BASE_URL`. The
approved script has to read that variable. This checker cannot see whether it
does. Do not put a production URL in the script or in the profile.

Declare the same thresholds in the script that the profile lists. The checker
does not compare them.

Do not add a CI job, scheduled job, or push hook in the app repository or in this
fork that runs `k6 run`. Upstream k6 workflows in `.github/workflows/` are
unchanged and are not an own-use trigger.

## How to read a later result

Until that later run exists, the status is `unexecuted` or `blocked`. Do not treat
either as a pass. `failed`, `aborted`, and `passed` are defined for a real
execution and are not emitted by the CLI.

When a real summary exists, k6 client metrics can show latency, error rate, and
throughput observed by the client. They can show that the client saw a failure.
They cannot show the server's root cause unless server telemetry was collected
for the same interval. The report field `serverObservability` stays `missing` for
every document this checker builds. Keep evidence and hypotheses apart. This
checker leaves `hypotheses` empty. It does not invent a p95, an error rate, or a
throughput number.

The improvement list is two process items: attach server telemetry before
interpreting client metrics (`retestSlot` `not-scheduled`), and the deferred
runner proof (`retestSlot` `deferred`). Those are not performance findings.
