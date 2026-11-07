# Per-app profile contract

The profile is one JSON object, schema version 1. The checker is
`ownuse/preflight.Load`. Unknown fields are rejected. There is no default target
and no default profile path.

The shape file `ownuse/preflight/testdata/profile-shape.json` shows the fields.
It is not a profile to execute. Its script path is not in this repository, and
the checker blocks the shape file itself because the file lives inside the fork.

## Identity

| Field | Rule |
| --- | --- |
| `app.name` | Lowercase slug for the web app. |
| `app.revision` | App revision supplied by that app. One token. |
| `environment.id` | Explicit slug for this isolated environment. `production`, `prod`, and `live` are rejected. |
| `environment.kind` | Exact string `nonproduction`. `production`, `prod`, and `live` are rejected. Any other value is rejected. |
| `environment.isolation` | Short description of how this environment is separated. |

`kind: nonproduction` is an explicit label. It does not prove the host is isolated
from production. This checker does not try to guess production from the hostname,
because that guess would be wrong in both directions.

## Script supplied by the app

| Field | Rule |
| --- | --- |
| `script.ref` | Versioned reference of the form `path/approved.js@v1`. |
| `script.sha256` | 64 lowercase hex characters for that approved file. |
| `script.path` | Absolute `.js` path on the operator machine. |

The checker does not read, parse, or run that file. It therefore cannot prove the
file contains the declared thresholds or that it reads `OWNUSE_TARGET_BASE_URL`.

## Limits and thresholds

| Field | Sanity ceiling |
| --- | --- |
| `limits.maxVUs` | 1 to 50 |
| `limits.maxDuration` | Go duration from `1s` to `5m` |
| `limits.maxIterations` | 1 to 5000 |
| `limits.maxRPS` | 1 to 100 |
| `thresholds` | At least one `{metric, expression}` pair. |

These ceilings only decide whether a profile can be planned. They are not a
measured safe load. The planned `k6 run` command passes `--rps` and does not pass
`--vus`, `--duration`, or `--iterations`. In this tree, `lib.Options.Apply` drops
script scenarios when a higher tier sets those shortcuts. Passing them would
replace the approved script's workload. The checker does not read the script, so
it also does not prove the script stays inside the VU, duration, and iteration
ceilings. A later `k6 inspect --execution-requirements` is how an operator looks
at that workload, and this tool does not launch it.

Threshold expressions are stored, not evaluated.

## Target and network hazards

`target.baseURL` is required. It must be absolute `http` or `https`, without
embedded credentials. Loopback and unspecified addresses are rejected. The host
must appear in `network.allowedHosts`.

| Field | Rule |
| --- | --- |
| `network.acknowledgeHazards` | Must be `true`. This records that redirects and DNS rebind were considered. It does not remove them. |
| `network.maxRedirects` | Required integer, 0 through 10. `0` means a later command would pass `--max-redirects 0`. |
| `network.dnsPolicy` | One of `preferIPv4`, `preferIPv6`, `onlyIPv4`, `onlyIPv6`, `any`. |
| `network.allowedHosts` | Explicit hosts. No patterns. |
| `network.blockHostnamePatterns` | At least one pattern accepted by k6 `HostnameTrie`. A pattern that matches an allowed host is rejected. |
| `network.blacklistCIDRs` | Optional. Each entry must parse with `lib.ParseCIDR`. |

k6 hostname patterns use an optional leading `*`, as in `*example.test`. The
checker calls `types.NewHostnameTrie` and `Contains`. That proves only what the
trie says about those strings. It does not prove a future process was started
with the flags, and a list of one blocked name does not block every other name.

The planned DNS flag is `ttl=0,select=first,policy=<dnsPolicy>`. `ttl=0` is the
upstream switch that disables the resolver cache. It does not stop a name from
being answered with a different address. CIDR blacklists are not a substitute:
the checker never resolves the target, so it cannot tell whether the target
address sits inside a listed range.

`--blacklist-ip` exists in upstream k6 for ranges an operator already knows.
Leaving `blacklistCIDRs` empty keeps the gap visible in `doesNotProve`. It does
not hide it behind a default range.

## Authorization and artifacts

| Field | Rule |
| --- | --- |
| `authorization.explicit` | Must be true. |
| `authorization.reference` | Non-empty change or ticket id. |
| `authorization.phase` | Exact string `end-of-development`. |
| `artifactsDir` | Absolute directory outside this fork. The checker does not create it. |

A valid profile is still `unexecuted`. Authorization allows a plan to be printed.
It does not start k6.

## Where the file lives

Keep the real profile outside the git work tree. The CLI resolves `--profile`,
`script.path`, and `artifactsDir` and blocks any of them that sit inside the
repository that contains `.git`. `.gitignore` ignores `/ownuse/profiles/` and
`/ownuse/artifacts/` as a backstop, not as a place to store them.
