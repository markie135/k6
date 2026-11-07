# CLI, container, and reports

The core path is the existing k6 CLI. The container path is the unchanged
`Dockerfile` in this repository, whose runtime stage sets `ENTRYPOINT ["k6"]`.
No image is built or started by this change.

## Commands that already exist

Verified against this tree, not by running them:

| Command | Source |
| --- | --- |
| `k6 version` | `internal/cmd/version.go` |
| `k6 inspect [file]` | `internal/cmd/inspect.go` |
| `k6 inspect --execution-requirements [file]` | same file; adds `totalDuration` and `maxVUs` |
| `k6 run` | `internal/cmd/run.go` |
| `--summary-mode=full` | `internal/cmd/runtime_options.go` (`compact`, `full`, `disabled`) |
| `--summary-export <file>` | same file; JSON end-of-test summary |
| `-o json=<file>` | `internal/cmd/config.go` and `internal/output/json` |
| `--no-usage-report` | `internal/cmd/config.go` |
| `--max-redirects` | `internal/cmd/options.go` |
| `--block-hostnames` | same file, parsed by `HostnameTrie` |
| `--dns` | same file, parsed by `DNSConfig` |
| `--blacklist-ip` | same file, parsed by `lib.ParseCIDR` |
| `--rps` | same file |

`k6 inspect` loads the script. Init code in that script can perform I/O. Treat
upstream inspect as a later, intentional step, not as an offline guarantee. The
preflight `inspect` mode never calls it.

Default trend columns, if the script does not override them, are `avg`, `min`,
`med`, `max`, `p(90)`, and `p(95)` (`lib.Options` `DefaultSummaryTrendStats`).
This plan does not pass `--summary-trend-stats`.

The local REST API is off unless a process is started with `--address`
(`internal/cmd/root.go`). The planned command does not pass `--address`. When a
future process does enable it, the existing routes are `GET /v1/status`,
`PATCH /v1/status` (`stopped: true` aborts; `paused` pauses), and `GET /v1/metrics`
(`api/v1/routes.go`). `k6 stats` is a hidden command that reads that API. None of
these are invoked here.

`k6 cloud` is out of scope. The planned argv is local `k6 run` with
`--no-usage-report`.

## Planned command

`dry-run` prints argv. A typical `k6 run` list is:

- `k6 run`
- `--no-usage-report`
- `--summary-mode=full`
- `--summary-export <artifacts>/<planned-id>/summary.json`
- `--out json=<artifacts>/<planned-id>/raw-metrics.json`
- `--rps <limits.maxRPS>`
- `--max-redirects <network.maxRedirects>`
- `--dns ttl=0,select=first,policy=<network.dnsPolicy>`
- `--block-hostnames <pattern>` repeated
- `--blacklist-ip <cidr>` repeated when the profile lists any
- `--tag` / `--env` for the environment id, app name, script sha256, target, and planned id
- the absolute script path

The shell string redirects stdout to `<artifacts>/<planned-id>/human-summary.txt`.
That redirect is how the human-readable summary is kept. k6 writes the JSON
summary and the raw samples itself. The checker creates none of these files.

`plannedRunID` looks like `planned-` plus 16 hex characters. It is a path name
reserved by the dry-run. It is not evidence that k6 ran.

Container shape, also not executed:

```text
docker build -t k6-ownuse:local .
docker run --rm --entrypoint k6 \
  -v <script-dir>:/scripts:ro \
  -v <artifacts-dir>:/artifacts \
  k6-ownuse:local \
  run <the same flags, with paths inside the mounts>
```

Build the image from this repo's Dockerfile so the binary matches this fork.
The published `grafana/k6` image is not assumed to be this commit.

## Functions in this package

| Function | Bound | Used by the CLI |
| --- | --- | --- |
| `Load` | Decode and validate bytes. No script read and no dial. | yes |
| `Plan` | Build argv for a valid profile. No process. | dry-run only |
| `ReportForProfile` | `blocked` or `unexecuted` only. No measurements. | yes |
| `Classify` | Pure status table. | no |
| `ReportFromEvidence` | Copies caller-supplied figures or refuses them. | no |
| execute | Not implemented. | no |
| AgentDesk start, status, results, cancel | Not implemented. | no |

`Classify` returns `unexecuted`, `blocked`, `failed`, `aborted`, or `passed`.
`passed` requires a completed execution, a summary, a positive metric count, no
abort, no exit error, and thresholds marked passed. An executed run with an empty
summary is `failed`. `ReportForProfile` returns an error if asked for `passed`.
`ReportFromEvidence` returns an error if measurements are attached to
`unexecuted` or `blocked`.

## Report fields

Every CLI report includes:

- `status`
- declared `thresholds` and `thresholdsEvaluated: false`
- `measurements.present: false`, with null latency, error rate, and throughput
- `provenance` (fork URL, parent, base commit, entrypoint date, app revision,
  script ref and sha256, environment id and kind)
- `resources` (the declared ceilings, `limitsEnforced: false`)
- `serverObservability: "missing"`
- `evidence` versus `hypotheses` (hypotheses are an empty array)
- `improvements` with priority and `retestSlot`
- `blockers`
- `doesNotProve`, including the redirect, DNS rebind, and client-metric limits

A pass with no samples is rejected by `Classify` and by `ReportFromEvidence`.
The CLI never emits `passed`.

## Connector

Do not add an AgentDesk adapter in this fork unless a later task justifies a thin
start, status, results, and cancel layer. If that happens, it should shell out to
the same CLI or container entrypoint, keep profiles outside this public fork, and
reuse the status names above. Cancel would map to the existing REST
`PATCH /v1/status` only for a process that was explicitly started with
`--address`. That adapter is not in this change, and this package does not import
or copy private AgentDesk code.

Runner execution proof remains `deferred` on `executionBoundary.runnerProof`.
