# Consumer Makefile — delegates canonical targets to .standards/templates/Makefile.canonical.
# Originally cloned from the pre-include-era templates/Makefile; migrated
# by migrate-makefile.sh. The original is preserved at Makefile.pre-migrate.bak.

ifeq (,$(wildcard .standards/templates/Makefile.canonical))
$(error .standards/templates/Makefile.canonical not found. Run: git submodule update --init --recursive)
endif

include .standards/templates/Makefile.canonical

## Consumer-specific content (preserved from the original Makefile).
## Targets whose recipes matched canonical were removed by the migration.
## Targets that drifted from canonical (see migration report) remain below
## and should be reconciled by hand.

# Coverage: exclude cmd/ entry points from the per-file coverage gate. keel is a
# root-module repo, so its LCOV paths are repo-root-relative (cmd/keel/main.go).
# The canonical default (/cmd/) only matches nested-module layouts that carry a
# leading slash (clients/x/cmd/), so it is a silent no-op here; (^|/)cmd/ matches
# both. cmd/*/main.go is exercised only via the built binary under BATS and
# integration tests, which are not coverage-instrumented — mirroring the Rust
# scope's src/bin/ exclusion. coverage-go.sh honors this override.
export GO_COVERAGE_IGNORE_REGEX := (^|/)cmd/
# Workflow step env that tool targets read reaches the dev image (scripts/dev/run.sh passes only
# the variables KEELCORE_DEV_ENV names): GOOS/GOARCH for the release cross-builds, GH_TOKEN for
# zizmor's online audits and gh attestation verify, the image index digests for release-verify.
export KEELCORE_DEV_ENV := GOOS GOARCH GH_TOKEN MAX_DIGEST MIN_DIGEST FIPS_DIGEST

.PHONY: all
.PHONY: clean
.PHONY: min
.PHONY: max
.PHONY: max-no-fips
.PHONY: build
.PHONY: integration-test
.PHONY: unit-test
.PHONY: test-consistency
.PHONY: test-integrity
.PHONY: test-compose
.PHONY: test-k8s
.PHONY: coverage
.PHONY: lint-go
.PHONY: lint-newlines
.PHONY: release-sbom
.PHONY: release-sign
.PHONY: release-binaries
.PHONY: release-ci
.PHONY: container-setup
.PHONY: container-deploy
.PHONY: container-test
.PHONY: container-stack-test
.PHONY: container-stack-test-keep
.PHONY: container-teardown
.PHONY: container-host-network
.PHONY: certs-image
.PHONY: gen-certs
.PHONY: gen-schema
.PHONY: install-hooks
.PHONY: fresh-repo
.PHONY: rulesets-check
.PHONY: rulesets-apply
.PHONY: help
.PHONY: help-local
.PHONY: pre-commit
.PHONY: keel-schema-regen
.PHONY: setup-bats
.PHONY: setup-pebble
.PHONY: setup-kind
.PHONY: setup-syft
.PHONY: setup-markdownlint
.PHONY: setup-ghcr
.PHONY: ci-pr-policy
.PHONY: ci-secret-scan
.PHONY: ci-dco
.PHONY: ci-coverage-delta
.PHONY: ci-smoke-windows
.PHONY: bats-integrity
.PHONY: bats-example
.PHONY: ci-build-example
.PHONY: clean-local
.PHONY: setup-bats-local
.PHONY: create-release-local
.PHONY: update-pins-local
.PHONY: release-images-local
.PHONY: native-build-local
.PHONY: native-integrity-local
.PHONY: dev-image-publish-local
.PHONY: coverage-report
.PHONY: audit
.PHONY: check-legal-drift
.PHONY: size-report
.PHONY: roadmap-issues
.PHONY: k8s-cluster-test
.PHONY: k8s-helm-deploy
.PHONY: k8s-stack-up
.PHONY: k8s-stack-down
.PHONY: k8s-kind-load
.PHONY: k8s-kind-setup
.PHONY: k8s-kind-teardown

# Default target: build the shredded minimalist binary
all: min

## Build Targets
min:
	@echo "⚓ Building Shredded (BYOS)..."
	bash scripts/dev/run.sh bash scripts/build/ci_min.sh

max:
	@echo "⚓ Building Rock-Hard (FIPS/Corporate)..."
	bash scripts/dev/run.sh bash scripts/build/ci_max.sh

max-no-fips:
	@echo "⚓ Building Ripped (Full Feature/No-FIPS)..."
	bash scripts/dev/run.sh bash scripts/build/ci_max_no_fips.sh

test-consistency:
	@echo "🧪 Running consistency suite..."
	bash scripts/dev/run.sh --writable bash scripts/test/consistency.sh

test-integrity:
	@echo "🧪 Running BATS integrity suite..."
	bash scripts/dev/run.sh bash scripts/build/ci_test_binary.sh

## test-example chains the existing build + bats targets (no inline steps).
test-example: ci-build-example bats-example
	@echo "🧪 Running examples/myapp tests..."

test-compose:
	@echo "🐳 Running Docker Compose integration tests (P3)..."
	./scripts/test/compose.sh

test-compose-keep:
	@echo "🐳 Running Docker Compose integration tests (stack stays up)..."
	./scripts/test/compose.sh --keep-up

## CI Kubernetes (kind)
test-k8s:
	@echo "⎈ Running kind k8s integration tests..."
	./scripts/test/k8s.sh

## P3.5 Local k8s (Apple container) Targets
container-setup:
	@echo "☸️  Starting local k8s cluster + keel-toolbox via Apple container..."
	./scripts/container/setup.sh

container-deploy:
	@echo "⎈ Building and deploying Keel to Apple container k8s..."
	./scripts/container/deploy.sh

container-test:
	@echo "🧪 Running k8s integration tests against Apple container cluster..."
	./scripts/container/test.sh

container-stack-test:
	@echo "🧪 Running compose integration tests on the Apple container k8s stack..."
	./scripts/container/stack-test.sh

container-stack-test-keep:
	@echo "🧪 Running stack tests (stack stays up)..."
	./scripts/container/stack-test.sh --keep-up

container-teardown:
	@echo "🧹 Tearing down Apple container k8s environment..."
	./scripts/container/teardown.sh

## Apple container host networking
container-host-network:
	@echo "🛣️  Checking Apple container host networking..."
	./scripts/container/host-network.sh

## CA image: full Mozilla bundle; keel images copy an explicit root allowlist from it
certs-image:
	@echo "🔐 Building keel-certs CA image..."
	./scripts/build/certs-image.sh

## Certificate Generation
gen-certs:
	@echo "🔑 Generating self-signed test certificates..."
	bash scripts/dev/run.sh bash scripts/test/gen-certs.sh

gen-schema:
	@echo "📐 Regenerating config field schema..."
	bash scripts/dev/run.sh --writable bash scripts/release/gen-schema.sh

## Repo Setup Targets
## Pre-commit composition: keel's schema-regen runs before the canonical
## pre-commit checks (format + lint + governance-gate). Pure prerequisite
## aggregator on the canonical `pre-commit` target — same pattern as `help`.
## install-hooks is inherited from canonical; it installs a hook that runs
## `make pre-commit`, so this prerequisite fires at commit time.
pre-commit: keel-schema-regen
keel-schema-regen:
	@echo "📐 Regenerating + staging config schema (keel-specific)..."
	bash scripts/hooks/schema-regen.sh

fresh-repo: install-hooks
	bash scripts/fresh-repo.sh

## Branch protection as code (docs/governance.md): check is read-only; apply needs an admin GH_TOKEN.
rulesets-check:
	@echo "🛡️  Checking rulesets against .github/rulesets..."
	bash scripts/dev/run.sh bash scripts/admin/rulesets.sh check

rulesets-apply:
	@echo "🛡️  Applying .github/rulesets..."
	bash scripts/dev/run.sh bash scripts/admin/rulesets.sh apply

## Universal Canonical Targets (language-independent interface; mandated by CI standards)
build: min

unit-test: test-unit

integration-test: test-integrity test-consistency

setup-pebble:
	@echo "🔧 Installing pebble..."
	bash scripts/dev/run.sh bash scripts/ci/setup-pebble.sh

setup-kind:
	@echo "🔧 Installing kind and creating cluster..."
	./scripts/ci/setup-kind.sh

## CI Gate Targets
ci-smoke-windows:
	@echo "🪟 Running Windows smoke test..."
	./scripts/build/ci_smoke_windows.sh

## BATS Runner Targets (artifact-based; distinct from test-integrity which builds first)
bats-integrity:
	@echo "🧪 Running BATS integrity suite against downloaded artifact..."
	bash scripts/dev/run.sh bash scripts/test/bats-integrity.sh

bats-example:
	@echo "🧪 Running BATS example suite..."
	bash scripts/dev/run.sh bash scripts/test/bats-example.sh

ci-build-example:
	@echo "🔨 Building examples/myapp (CI, no BATS)..."
	bash scripts/dev/run.sh bash scripts/build/ci_example.sh

## release-binaries: every flavor for every architecture, concurrently, under its release name.
release-binaries:
	@echo "⚓ Building release binaries..."
	bash scripts/dev/run.sh bash scripts/release/build-binaries.sh

## Kubernetes Helper Targets (internal; called by test-k8s)
k8s-cluster-test:
	@echo "⎈ Running k8s cluster tests..."
	./scripts/k8s/cluster-test.sh

k8s-helm-deploy:
	@echo "⎈ Deploying Helm chart to kind cluster..."
	./scripts/k8s/helm-deploy.sh

k8s-stack-up:
	@echo "⎈ Deploying the compose-equivalent k8s stack..."
	./scripts/k8s/stack.sh up

k8s-stack-down:
	@echo "🧹 Removing the compose-equivalent k8s stack..."
	./scripts/k8s/stack.sh down

k8s-kind-load:
	@echo "⎈ Loading image into kind cluster..."
	./scripts/k8s/kind-load.sh

k8s-kind-setup:
	@echo "⎈ Setting up kind cluster..."
	./scripts/k8s/kind-setup.sh

k8s-kind-teardown:
	@echo "⎈ Tearing down kind cluster..."
	./scripts/k8s/kind-teardown.sh

setup-ghcr:
	@echo "🔧 Setting up GHCR credentials..."
	./scripts/release/setup-ghcr.sh

## Additional Release Targets
release-ci:
	@echo "🚀 Running release CI pipeline..."
	./scripts/release/ci.sh

## Additional Utility Targets
size-report:
	@echo "📏 Generating binary size report..."
	bash scripts/dev/run.sh bash scripts/build/size-report.sh

roadmap-issues:
	@echo "🗺️  Syncing roadmap issues..."
	./scripts/roadmap-issues.sh

## Utility Targets
# Host uid:gid on Linux, where the dev image runs as root (dist-chmod hands dist/ back); empty elsewhere.
## Local artifact cleanup — also delegated to by canonical clean.sh (`make clean`)
clean-local:
	@echo "🧹 Removing keel build artifacts (dist/, target/)..."
	bash scripts/clean.local.sh

## BATS tool extras (timeout) — also delegated to by canonical setup-bats.sh (`make setup-bats`)
setup-bats-local:
	@echo "⏱️  Installing keel BATS extras (timeout)..."
	bash scripts/ci/setup-bats.local.sh

## Schema-diff bump hook (PREV_TAG=vX.Y.Z) — also called by canonical create-release.sh (`make create-release`)
create-release-local:
	@echo "📐 Computing bump level from the config schema diff..."
	bash scripts/release/create-release.local.sh $(PREV_TAG)

## The CA bundle image keel's images copy from: run by make release-images, and runnable alone.
release-images-local:
	@echo "🐳 Building the CA bundle image..."
	bash scripts/release/docker.local.sh

## keel's part of the canonical native targets: run by them, and runnable alone.
native-build-local:
	@echo "🔨 Building keel and pebble for $(OS)/$(ARCH)..."
	bash scripts/dev/run.sh bash scripts/test/native-build.local.sh $(OS) $(ARCH) target/native/$(OS)-$(ARCH) $(EXT)

native-integrity-local:
	@echo "🧪 Running the integrity suite against dist/..."
	bash scripts/test/native-integrity.local.sh

## Pin hook (WHAT=pins|holds) — also called by canonical update-pins.sh (`make update-pins`)
update-pins-local:
	@echo "📌 keel pin hook: $(WHAT)"
	bash scripts/release/update-pins.local.sh $(WHAT)

dev-image-publish-local:
	@echo "📦 keel publish hook: $(WHAT)"
	bash scripts/dev/publish.local.sh $(WHAT)

## coverage-report: pretty-print the merged per-file LCOV (from `make coverage`)
## as an ascending, colorized console table with coverage bars. Read-only view;
## does not run tests or alter the baseline.
coverage-report:
	bash scripts/dev/run.sh bash scripts/test/coverage-report.sh

## help composes keel's curated summary (help-local) with the canonical
## auto-generated target list. help-local is a pure prerequisite aggregator
## (no recipe on `help` itself), so Make runs help-local first, then the
## canonical `help` recipe — no recipe-override, and governance-refresh treats
## the aggregator as non-drifting.
help: help-local

help-local:
	@echo "Keel Build System"
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build:"
	@echo "  min             Build the minimalist <3MB binary (Default)"
	@echo "  max             Build the FIPS-compliant static binary"
	@echo "  max-no-fips     Build the full-feature non-FIPS binary"
	@echo ""
	@echo "Test:"
	@echo "  test             Run unit + consistency + BATS integrity tests"
	@echo "  test-consistency Consistency suite only"
	@echo "  test-integrity   BATS integrity suite only"
	@echo "  test-compose     Docker Compose integration tests (P3)"
	@echo "  test-compose-keep  Same but leave compose stack running"
	@echo "  test-k8s         kind k8s integration tests (requires Docker)"
	@echo "  coverage         Generate coverage report (total %%, uncovered, total lines)"
	@echo ""
	@echo "Local k8s (Apple container / P3.5):"
	@echo "  container-setup           Start k8s cluster + keel-toolbox via Apple container"
	@echo "  container-deploy          Build keel:test + helm install to the cluster"
	@echo "  container-test            Run k8s integration tests"
	@echo "  container-stack-test      Run compose suite on the k8s stack (hostPorts, in-node)"
	@echo "  container-stack-test-keep Same but leave the stack running"
	@echo "  container-teardown        Uninstall + delete toolbox and cluster"
	@echo "  container-host-network    Check/enforce the host<->guest bridge route"
	@echo ""
	@echo "Lint:"
	@echo "  lint            gofmt check + go vet + staticcheck"
	@echo "  lint-go         gofmt check + go vet + staticcheck"
	@echo ""
	@echo "Release:"
	@echo "  release-sbom              Generate SBOM (requires syft)"
	@echo "  release-sign              Sign artifacts (requires cosign)"
	@echo "  release-binaries          Build every release binary, concurrently"
	@echo "  gen-schema                Regenerate pkg/config/schema.yaml from config.Config"
	@echo "  create-release            Compute and tag next semver release"
	@echo "  create-release FORCE=v1.0.0  Force a specific version"
	@echo "  create-release DRY_RUN=1  Compute and show the next tag; no tag, no push"
	@echo ""
	@echo "Setup (CI tooling):"
	@echo "  setup-bats        Install bats-core"
	@echo "  setup-pebble      Install pebble ACME test server"
	@echo "  setup-kind        Install kind and create cluster"
	@echo "  setup-markdownlint Install markdownlint-cli"
	@echo "  setup-staticcheck Install staticcheck linter"
	@echo "  setup-syft        Install syft SBOM tool"
	@echo "  setup-cosign      Install cosign signing tool"
	@echo ""
	@echo "CI Gates:"
	@echo "  ci-pr-policy      Run PR policy check"
	@echo "  ci-secret-scan    Run secret scan"
	@echo "  ci-dco            Run DCO sign-off check"
	@echo "  ci-coverage-delta Check coverage delta vs base branch"
	@echo "  ci-smoke-windows  Smoke test the Windows binary (built by native-build)"
	@echo "  ci-build-example  Build examples/myapp only (no BATS)"
	@echo "  bats-integrity    Run BATS integrity suite (artifact must exist)"
	@echo "  bats-example      Run BATS example suite"
	@echo ""
	@echo "Repo Setup:"
	@echo "  fresh-repo      First-time setup: download deps + install hooks"
	@echo "  install-hooks   Install git hooks from scripts/hooks/"
	@echo ""
	@echo "Utilities:"
	@echo "  audit             Run CI/Makefile standards compliance audit"
	@echo "  gen-certs         Generate self-signed TLS certs for testing"
	@echo "  check-legal-drift Check legal file drift"
	@echo "  size-report       Generate binary size report"
	@echo "  roadmap-issues    Sync roadmap issues"
	@echo "  lint-newlines     Check trailing newlines"
	@echo "  clean             Remove build artifacts"
	@echo ""
	@echo "Universal targets (canonical; language-independent):"
	@echo "  build             Alias for min (default artifact)"
	@echo "  unit-test         Alias for test-unit"
	@echo "  integration-test  Alias for test-integrity"
