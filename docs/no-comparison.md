# Keel: The "No Comparison" Guide

There are absolutely credible tools in the cloud-native ecosystem, but the battleground for brownfield
applications is rarely about *features* — it is about **form factor and operational friction**.

Keel is not inventing OpenTelemetry, JWT validation, or FIPS compliance. It is inventing a hyper-specific
delivery mechanism: a 7 MB, statically compiled, control-plane-free sidecar.

When evaluating Keel against the landscape, here is why traditional alternatives cause pain for SREs, Platform
Engineers, and Compliance teams, and how Keel displaces them.

---

## 1. The Heavyweights: Envoy & Service Meshes (Istio, Linkerd)

If a Platform Engineering team is trying to solve tracing, auth, and metrics fleet-wide, this is usually their
first stop.

**The Overlap:** Envoy (and the meshes that use it) can handle JWT validation, export OpenTelemetry, expose
Prometheus metrics, and query OPA.

**The Friction:** Envoy is notoriously complex and resource-heavy. Adopting Istio to get basic observability
and zero-trust on a brownfield app requires deploying a massive, cluster-wide control plane. For federal teams
needing FIPS, they usually have to buy an expensive, vendor-supported enterprise build of Envoy (Tetrate,
Solo.io).

**Keel's Advantage:** Keel requires zero control plane. The Helm chart defaults to sidecar mode — point it at
your existing service image and it deploys. No CRDs. No control plane APIs. No policy DSL. A single static
binary that opts into features via build tags.

---

## 2. The Web Servers: Caddy & NGINX

SREs and self-hosted infrastructure teams often lean on traditional web servers to act as reverse proxies in
front of legacy apps.

**The Overlap:** Caddy is famous for its automatic Let's Encrypt (ACME) integration. NGINX is the ubiquitous
drop-in reverse proxy for basic routing and TLS termination.

**The Friction:** Caddy is not designed for strict federal compliance and does not have a native, compiled-in
FIPS 140 posture. NGINX requires bolting on brittle third-party modules, Lua scripts, or NJS to handle
OpenTelemetry or OPA integration, turning the proxy into a maintenance nightmare.

**Keel's Advantage:** Keel compiles OTel and FIPS directly into the binary. For authorization, Keel's `authz`
build calls OPA over a Unix socket — sub-millisecond, no network hop, full Rego policy with a decision audit
log. OPA stays OPA; Keel is the transparent proxy boundary in front of it. There are no dynamic plugins to
maintain or break during an upgrade.

---

## 3. The Single-Trick Ponies: OAuth2-Proxy & Ghostunnel

DevSecOps teams rushing to meet zero-trust or mTLS mandates often reach for hyper-focused, single-use
sidecars.

**The Overlap:** OAuth2-Proxy is a very common sidecar injected specifically to intercept traffic and validate
tokens before hitting the app. Ghostunnel is used similarly to wrap legacy apps in TLS.

**The Friction:** They only solve one problem. If you deploy OAuth2-Proxy for auth, you still have to figure
out how to get Prometheus metrics and OpenTelemetry spans out of the legacy app. This leads to sidecar sprawl,
where a single pod has three different sidecars eating up compute.

**Keel's Advantage:** Keel consolidates the sprawl. It handles TLS, identity validation, authorization, and
observability in a single binary. The min flavor ships under 8 MB; the full-featured build is approximately
10 MB — either is a fraction of what three separate sidecars would cost.

---

## The True Competitor: The Status Quo

The reality of the brownfield market is that Keel's true competitor is not another sidecar. It is
**"do nothing"** — teams accepting the risk, failing the audit, or absorbing the operational toil because
deploying an Envoy mesh or rewriting the app is politically or technically impossible.

Keel eliminates the excuses for the status quo.
