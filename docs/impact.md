# Keel: This Is for You, If …

Keel is not a general-purpose API gateway, a WAF, or a service mesh.
It is a **brownfield sidecar** — a hardened, embeddable N/S boundary layer you drop next to an existing service without touching its code, its build pipeline, or its team.

Each capability below is independently opt-in via build tags and Helm values.
If any of the following sentences describes your week, keel is worth ten minutes of your time.

---

## This is for you, if …

**… your tracing coverage is a patchwork** because you can't get app teams to instrument 40 services on a deadline. Keel's `otel` build injects OpenTelemetry spans at the boundary — uniform, enforced, no app rebuild required.

**… you get paged blind** because a brownfield service has no `/metrics` and Prometheus has nothing to scrape. Keel's `prom` build exposes HTTP golden signals (latency, error rate, saturation) the moment the sidecar starts.

**… mobile or gaming clients are hitting you with TCP head-of-line blocking** on lossy networks. Keel's `h3` build adds QUIC/HTTP3 termination as a sidecar without touching the origin service.

**… you're under a zero-trust mandate and 40 app teams each have a different timeline.** Keel's `authn` build enforces JWT at the pod boundary fleet-wide — one Helm values change per service, no coordination required.

**… your on-call rotation includes 2am cert expiry alerts.** Keel's `acme` build manages the full Let's Encrypt lifecycle automatically, with cache-on-restart so a pod bounce never triggers an unnecessary CA round-trip.

**… authorization logic is duplicated across services and has already diverged.** Keel's `authz` build calls OPA over a Unix socket — sub-millisecond, no network hop, full Rego policy with a decision audit log your compliance team can actually read.

**… FedRAMP, FISMA, HIPAA, or a DoD IL4/IL5 auditor is asking you to prove your TLS stack is FIPS 140 validated.** Keel's `fips` flavor image is the only lightweight Go N/S sidecar that ships with FIPS 140 enforced at compile time. No separate proxy procurement. No 6-week change board.

**… your SIEM requires every boundary request to ship to a syslog or HTTP sink** and the app team can't be the one to do it. Keel's `remotelog` build forwards logs from the sidecar's traffic path — tamper-resistant by design, because it runs outside the app process.

**… your services keep failing security scanner audits on missing security headers** (X-Frame-Options, CSP, HSTS). Keel's `owasp` build injects the full OWASP recommended header set at the boundary, fleet-wide, before the scanner sees a single response.

**… you need to wrap a service you don't own** — an acquired company, a vendor image, a legacy container — and adding TLS, auth, and observability is otherwise a cross-team negotiation. Keel's `mode: sidecar` Helm value wraps any container image with no changes to the image itself.

**… you're shipping to customer clusters under FedRAMP scope** and need to prove every pod in scope runs a FIPS-validated crypto module. Keel's `flavor: fips` Helm value is a single-line declaration that satisfies the auditor, pinned to a reproducible, vendor-committed build.

---

## Feature × Community Impact

The table below maps each keel capability to the three communities that feel its absence most acutely — and gain the most from it as a brownfield sidecar.

| Feature | Top Community #1 | Top Community #2 | Top Community #3 |
|---|---|---|---|
| **otel** | SRE teams retrofitting tracing onto legacy services — recompile forbidden, app team unavailable | Platform orgs standardizing OTel across heterogeneous fleets — keel injects spans uniformly without touching each service | FinTech audit/compliance teams who need request trace provenance at the boundary, not from app instrumentation they don't control |
| **prom** | K8s platform teams with brownfield services that have zero `/metrics` — keel exposes HTTP golden signals without touching app code | SREs who get paged blind because their service has no scrape target; keel gives them latency/error rate immediately | DataDog/New Relic migrants moving to Prometheus — keel provides a scrape endpoint even for services still mid-migration |
| **h3** | Mobile backend teams hit by TCP head-of-line blocking on lossy networks — QUIC built in, no app rebuild | Gaming backends needing sub-10ms connection establishment at scale — H3 eliminates TLS+TCP handshake stacking | CDN/edge operators running origin fleets that can't be recompiled — keel adds QUIC termination as a drop-in sidecar |
| **authn** | Multi-tenant SaaS platforms with legacy services that have no auth layer — keel enforces JWT at the boundary before one packet reaches the app | DevSecOps teams under zero-trust mandates who can't wait for 40 app teams to ship auth — sidecar unblocks the mandate fleet-wide | Acquired/merged company integrations where the acquired service has incompatible auth — keel normalizes identity at ingress |
| **acme** | Self-hosted infra ops teams drowning in manual cert renewals and 2am expiry incidents — ACME + PVC cache handles it automatically | Healthcare/SME orgs with no dedicated PKI team — keel provides a full cert lifecycle with zero infrastructure overhead | GitOps shops (Flux/ArgoCD) who want cert lifecycle declarative and version-controlled — `cachePVC` + Helm values covers it |
| **authz** (OPA) | Platform teams where authorization logic is duplicated across 30 services and diverges — OPA Unix socket call centralizes policy without touching any app | Compliance-heavy orgs needing a full decision audit log for every request — OPA's decision log pipeline is a hard FedRAMP/SOC2 requirement | Multi-tenant SaaS with tenant-level policy isolation needs — Rego handles attribute-level authz that mesh policies can't express |
| **fips** | Federal contractors under FedRAMP/FISMA mandates — `fips` flavor image is the only Go N/S sidecar that ships FIPS 140 out of the box | Healthcare orgs with HIPAA + state-level data residency requirements where the TLS stack itself must be FIPS — no other lightweight sidecar option exists | Defense contractors and cleared facilities running air-gapped k8s — vendor-committed, FIPS-tagged, no external pulls required |
| **remotelog** | Orgs with centralized syslog/SIEM infrastructure (Splunk, QRadar) that mandate all boundary traffic logs go there — keel's remote sink requires zero app changes | Security teams that need a tamper-resistant log stream separate from app stdout — sidecar log path can't be suppressed by app code | Regulated industries where log forwarding to an immutable sink is a compliance control — HTTP or syslog sink satisfies the auditor without app involvement |
| **owasp** | Orgs with PCI-DSS scope — OWASP headers are a checkbox requirement; keel adds them fleet-wide without touching a single app | Dev teams that keep failing security scanner audits on headers (X-Frame-Options, CSP, etc.) — keel injects them at the boundary | B2B SaaS vendors whose enterprise customers run header-scanning tools pre-onboarding — keel makes every service pass on day 1 |
| **Helm `mode: sidecar`** | Platform teams that can't modify app Dockerfiles or build pipelines — `sidecar.app.image` is the only change required to wrap any container | GitOps orgs adopting zero-trust incrementally — they patch `values.yaml` per service in PRs, no app team coordination needed | ISVs shipping to customer k8s clusters — they add keel as a sidecar in their Helm chart; customers get TLS/authn/authz without any operator-level changes |
| **Helm `flavor: fips`** | FedRAMP-authorized SaaS vendors who must prove every pod in scope runs a FIPS-validated crypto module — single `flavor:` field satisfies the auditor | DoD IL4/IL5 operators who need a bill of materials showing no non-FIPS crypto in the N/S path — keel's build-tag approach makes this provable | Air-gapped government clusters where pulling a separate FIPS-hardened proxy image is normally a 6-week procurement process — keel collapses it to a values change |

---

## Implementation: The 7MB Drop-In

Keel is designed to be invisible to your app code and weightless in your cluster. With a **shredded 7MB footprint** and ripped of legacy overhead, rolling it out is a purely operational exercise.

* **Zero-Touch Retrofit:** Inject it via `mode: sidecar` in your Helm chart. The application container remains completely untouched.
* **Compile-Time Hardening:** Capabilities are explicitly opted-in via build tags, not loaded as brittle dynamic plugins. You ship a single, statically linked binary.
* **Instant Telemetry:** The moment the pod schedules, `prom` and `otel` are active at the boundary. No waiting for app teams to patch or update dependencies.

## AI-Friendly Guardrails

When you are exposing LLMs, internal AI agents, or expensive inference endpoints, the N/S boundary becomes your primary defense against compute exhaustion, unauthorized access, and compliance breaches. Keel acts as a hardened shield for AI workloads:

* **Pre-Inference Identity Validation (`authn`):** Never let an unauthenticated request reach a GPU or an expensive LLM API. Keel verifies JWTs at the boundary in microseconds, dropping unverified requests before they consume a single compute cycle.
* **Auditable AI Access (`authz` + `remotelog`):** Compliance teams heavily scrutinize who is talking to internal AI models. Keel uses OPA to strictly verify model access per-tenant, while simultaneously shipping a tamper-proof log of the request to your SIEM.
* **FedRAMP-Ready AI (`fips`):** Shipping AI to federal, healthcare, or DoD customers requires strict network-layer compliance. Keel’s `fips` flavor allows you to wrap standard AI containers in a validated crypto boundary with a single Helm value change.
* **Token Stream Resiliency (`h3`):** Streaming long-form AI token responses back to edge or mobile clients? Keel's HTTP/3 termination ensures dropped packets on lossy networks don't stall the entire response stream.

## Standards: Uncompromising Supply Chain Provenance

When you deploy a N/S boundary layer into a regulated environment, the binary's provenance is just as critical as its feature set. Keel's engineering standards eliminate supply-chain ambiguity:

* **OpenSSF Baseline Certification:** Keel tracks and adheres to the Open Source Security Foundation's (OpenSSF) baseline criteria for secure software development. Vulnerability management, automated testing, and secure coding practices are treated as zero-tier requirements, not afterthoughts.
* **Provable & Reproducible Builds:** Every release is pinned to a verifiable, reproducible build pipeline. Keel natively supports providing a comprehensive Software Bill of Materials (SBOM), satisfying federal (EO 14028) and strict enterprise software supply chain mandates.
* **SLSA Hardened:** From commit to container registry, the build pipeline is hardened against tampering. This gives your DevSecOps and platform teams mathematical confidence that the sidecar they deploy is exactly the sidecar compiled.
* **Continuous Cryptographic Validation:** The `flavor: fips` image doesn't just bundle a compliant crypto library; its development standards ensure it is validated against FIPS 140 compliance suites to guarantee no non-approved ciphers ever leak into the N/S traffic path.

---

## What keel is not

- **Not a WAF.** Keel does not inspect or block payloads based on signatures. Use a dedicated WAF upstream if you need that.
- **Not a service mesh.** Keel owns N/S (external → pod). Istio and Linkerd own E/W (pod → pod). They are complementary; keel does not replace them.
- **Not a policy engine.** Keel calls OPA for authorization decisions. OPA is the policy engine; keel is the enforcement point.
- **Not an ingress controller.** Keel runs inside the pod, not at the cluster edge. It is the last hop before your app, not the first hop from the internet.
- 