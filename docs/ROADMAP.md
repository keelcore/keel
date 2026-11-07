# Keel Roadmap

This document tracks planned future capabilities that are not yet implemented. Items are drawn from the
project's P18 open backlog. Each item includes context explaining what it is, why it is on the roadmap, and
what problem it solves.

This roadmap reflects intent, not commitment. Priorities may shift based on community feedback and real-world
usage patterns.

> **Maintainer note:** Each entry below is mirrored as a GitHub issue labelled `roadmap`.
> To sync new entries after editing this file, run:
>
> ```sh
> scripts/create-roadmap-issues.sh
> ```
>
> The script is idempotent — it skips entries that already have a matching issue (open or closed).

---

## Planned Items

### "Keel-Haul" CLI — Standalone TUI Companion

**What it is:** A terminal UI (TUI) companion tool that wraps local processes with Keel's security posture,
similar to how you would run a process under a supervisor.

**Why it matters:** Currently, "keel-hauling" a legacy service (forcing it into modern security compliance
without rewriting it) requires configuring Keel in sidecar mode and running both processes, typically via
Docker Compose or a process manager. The Keel-Haul CLI would make this a one-command operation:
`keel-haul ./my-legacy-binary --port 3000`. It would start the upstream process, start Keel in sidecar mode
pointed at it, and provide a live TUI showing traffic, health, and metrics.

**Target users:** Developers who want to locally test what their service looks like behind Keel's security
layer, and operators who want to quickly wrap a legacy process on a VM without setting up Kubernetes.

---

### "Verified Secure" Badge Service

**What it is:** A service that generates a scorecard and embeddable badge showing which Keel build tags are
active in a given binary, and what security posture that implies.

**Why it matters:** A repository badge like
"[![Keel: Full Security](https://badge.keel.dev/my-binary)](https://badge.keel.dev/my-binary)" provides
instant, verifiable signal that a project is running with OWASP hardening, FIPS crypto, authn, and no
disabled security features. The badge links to a scorecard page with details.

**Technical approach:** The binary embeds its build tags (this is already supported via the `/version` admin
endpoint). The badge service queries a deployed instance's `/version` endpoint and generates the scorecard.
For offline use, the badge can be generated from a binary artifact at build time.

---

### Build-a-Binary Web UI

**What it is:** A web configurator for generating `go build` commands with the correct build tags for your
use case.

**Why it matters:** Keel's build tag system is powerful but requires knowing all the available tags and their
implications. A web UI would let you check boxes ("I need FIPS," "I don't need HTTP/3," "I don't need
StatsD"), see the resulting binary size estimate, and get a copy-paste `go build` command.

**Example output:**

```sh
# For: FIPS + minimal observability, no H3, no StatsD
GOFIPS140=v1.0.0 go build \
  -tags 'no_h3,no_statsd,no_remotelog' \
  ./cmd/keel
# Estimated binary size: ~4–5 MB
```

---

### WASM Middleware Extension Points

**What it is:** Extension points that allow loading WebAssembly (WASM) modules as middleware handlers.

**Why it matters:** Keel's build-time opt-out model is powerful but requires recompiling to change behavior.
WASM middleware would allow extending Keel's request pipeline at runtime, without recompiling. A WASM module
could implement custom authn logic, request transformation, rate limiting, or content inspection.

**Prior art:** Envoy's WASM filter mechanism, Traefik's WASM middleware support.

**Constraint:** WASM middleware execution has measurable overhead. It is appropriate for low-traffic,
high-complexity use cases, not high-throughput data paths.

---

### TRADEMARK.md Periodic Review

**What it is:** A scheduled periodic review (annually) of TRADEMARK.md to ensure the trademark policy is
current and reflects actual usage in the ecosystem.

**Why it matters:** As the project grows and downstream users build on Keel, the trademark policy needs to
evolve. What constitutes "permitted use" and "restricted use" should reflect real-world patterns, not
assumptions made at project inception.

---

### Keel-Wrapped Helm Charts Architecture

**What it is:** A library of drop-in secure Helm chart replacements that wrap existing upstream charts with a
Keel sidecar container — without modifying upstream chart templates. Each wrapper chart ships as an OCI
artifact at `ghcr.io/keel/helm/keel-<name>`.

**Why it matters:** Operators running commodity workloads (nginx, Grafana, MinIO, Keycloak, etc.) currently
have no path to adding TLS termination, JWT authentication, OWASP headers, request protection, and
observability without forking upstream charts or adding a separate ingress layer. A wrapper chart is a
strict-dependency overlay: the upstream chart is vendored unchanged as a Helm dependency; the wrapper
contributes only a second Deployment (Keel + upstream container in the same pod) and a Service that exposes
Keel exclusively.

**Design invariants:**

1. The upstream chart is a declared `dependencies:` entry. Its templates are never modified.
2. Upstream runtime behaviour is controlled only via `values.yaml` overrides passed through to the
   dependency.
3. Wrapper `values.yaml` is a strict superset of upstream values: every upstream field is preserved under
   its upstream key; Keel-specific fields live under a top-level `keel:` namespace.
4. Wrapper chart `version` matches the upstream dependency `version` exactly (same semver).
5. Traffic flows: client → Keel container (external port) → `localhost` → upstream container (internal
   port). The upstream container is never exposed by the Service.
6. The Keel container image digest must be attested as a cosign OCI signature on every chart release,
   matching the attestation produced by the main Keel binary release pipeline.

**Pod structure:**

```text
Pod
 ├─ keel      (listens on external port, e.g. 8443)
 └─ upstream  (binds 127.0.0.1:<internal port>)
```

**OCI registry layout:** `ghcr.io/keel/helm/keel-<name>` — installed as
`helm install <name> oci://ghcr.io/keel/helm/keel-<name>` instead of the upstream
`helm install <name> <repo>/<name>`.

**Initial target charts (tracked individually as child issues):** nginx, grafana, prometheus, minio, argo-cd,
loki, tempo, elasticsearch, kibana, keycloak.
