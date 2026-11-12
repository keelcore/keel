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
# zizmor's online audits and gh attestation verify, the image index digests for verify-release.
export KEELCORE_DEV_ENV := GOOS GOARCH GH_TOKEN MAX_DIGEST MIN_DIGEST FIPS_DIGEST
# set-dist-permissions hands dist/ back to DIST_OWNER, which the canonical recipe no longer passes into the
# dev image; exported, run.sh forwards it (set-dist-permissions.sh imports it). Remove once canonical passes it.
export DIST_OWNER
# Canonical targets that run a tool image or Apple container without ensuring the container runtime first
# (keelcore/standards#32): make merges these prerequisites into the canonical rules. Remove once canonical has them.
update-pins: | ensure-container
publish-dev-image: | ensure-container
push-dev-image: | ensure-container
merge-dev-image: | ensure-container
pull-dev-image: | ensure-container
update-dev-image-ref: | ensure-container
publish-release-images: | ensure-container

.PHONY: build-all
.PHONY: clean
.PHONY: build-min
.PHONY: build-max
.PHONY: build-max-no-fips
.PHONY: build
.PHONY: test-integration
.PHONY: test-consistency
.PHONY: test-integrity
.PHONY: test-compose
.PHONY: test-k8s
.PHONY: test-coverage
.PHONY: lint-go
.PHONY: lint-newlines
.PHONY: generate-sbom
.PHONY: sign-release
.PHONY: build-release-binaries
.PHONY: run-release-ci
.PHONY: setup-container
.PHONY: deploy-container
.PHONY: test-container
.PHONY: test-container-stack
.PHONY: test-container-stack-keep
.PHONY: teardown-container
.PHONY: setup-container-host-network
.PHONY: build-certs-image
.PHONY: build-mockupstream-image
.PHONY: generate-certs
.PHONY: generate-schema
.PHONY: install-hooks
.PHONY: create-fresh-repo
.PHONY: check-rulesets
.PHONY: apply-rulesets
.PHONY: help
.PHONY: help-local
.PHONY: run-pre-commit
.PHONY: generate-schema-staged
.PHONY: setup-bats
.PHONY: setup-pebble
.PHONY: setup-kind
.PHONY: setup-syft
.PHONY: setup-markdownlint
.PHONY: setup-ghcr
.PHONY: check-pr-policy
.PHONY: check-secrets
.PHONY: check-dco
.PHONY: check-coverage-delta
.PHONY: test-smoke-windows
.PHONY: test-integrity-artifact
.PHONY: test-example-artifact
.PHONY: build-example
.PHONY: clean-local
.PHONY: lint-all-local
.PHONY: create-release-local
.PHONY: update-pins-local
.PHONY: publish-release-images-local
.PHONY: build-native-local
.PHONY: lint-gosec-local
.PHONY: lint-helm-schema
.PHONY: lint-test-layout
.PHONY: test-native-integrity-local
.PHONY: build-dev-image
.PHONY: publish-dev-image-local
.PHONY: report-coverage
.PHONY: audit
.PHONY: check-legal-drift
.PHONY: report-size
.PHONY: create-roadmap-issues
.PHONY: test-k8s-cluster
.PHONY: deploy-k8s-helm
.PHONY: deploy-k8s-stack
.PHONY: teardown-k8s-stack
.PHONY: load-k8s-kind
.PHONY: setup-k8s-kind
.PHONY: teardown-k8s-kind

# Default target: build the shredded minimalist binary
build-all: build-min

## Build Targets
build-min: | ensure-container
	@echo "⚓ Building Shredded (BYOS)..."
	bash scripts/dev/run.sh bash scripts/build/build-min.sh

build-max: | ensure-container
	@echo "⚓ Building Rock-Hard (FIPS/Corporate)..."
	bash scripts/dev/run.sh bash scripts/build/build-max.sh

build-max-no-fips: | ensure-container
	@echo "⚓ Building Ripped (Full Feature/No-FIPS)..."
	bash scripts/dev/run.sh bash scripts/build/build-max-no-fips.sh

test-consistency: | ensure-container
	@echo "🧪 Running consistency suite..."
	bash scripts/dev/run.sh --writable bash scripts/test/test-consistency.sh

test-integrity: | ensure-container
	@echo "🧪 Running BATS integrity suite..."
	bash scripts/dev/run.sh bash scripts/build/test-integrity.sh

## test-example chains the existing build + bats targets (no inline steps).
test-example: build-example test-example-artifact
	@echo "🧪 Running examples/myapp tests..."

test-compose:
	@echo "🐳 Running Docker Compose integration tests (P3)..."
	./scripts/test/test-compose.sh

test-compose-keep:
	@echo "🐳 Running Docker Compose integration tests (stack stays up)..."
	./scripts/test/test-compose.sh --keep-up

## CI Kubernetes (kind)
test-k8s:
	@echo "⎈ Running kind k8s integration tests..."
	./scripts/test/test-k8s.sh

## P3.5 Local k8s (Apple container) Targets
setup-container: | ensure-container
	@echo "☸️  Starting local k8s cluster + keel-toolbox via Apple container..."
	./scripts/container/setup.sh

deploy-container: | ensure-container
	@echo "⎈ Building and deploying Keel to Apple container k8s..."
	./scripts/container/deploy.sh

test-container: | ensure-container
	@echo "🧪 Running k8s integration tests against Apple container cluster..."
	./scripts/container/test.sh

test-container-stack: | ensure-container
	@echo "🧪 Running compose integration tests on the Apple container k8s stack..."
	./scripts/container/test-container-stack.sh

test-container-stack-keep: | ensure-container
	@echo "🧪 Running stack tests (stack stays up)..."
	./scripts/container/test-container-stack.sh --keep-up

teardown-container: | ensure-container
	@echo "🧹 Tearing down Apple container k8s environment..."
	./scripts/container/teardown.sh

## Apple container host networking
setup-container-host-network: | ensure-container
	@echo "🛣️  Checking Apple container host networking..."
	./scripts/container/setup-container-host-network.sh

## CA image: full Mozilla bundle; keel images copy an explicit root allowlist from it
build-certs-image:
	@echo "🔐 Building keel-certs CA image..."
	./scripts/build/build-certs-image.sh

## Mock upstream image: compiled from this tree in the dev image; the sidecar stack tests run it
build-mockupstream-image: | ensure-container
	@echo "🐳 Building keel-mockupstream image..."
	./scripts/build/build-mockupstream-image.sh

## Certificate Generation
generate-certs: | ensure-container
	@echo "🔑 Generating self-signed test certificates..."
	bash scripts/dev/run.sh bash scripts/test/generate-certs.sh

generate-schema: | ensure-container
	@echo "📐 Regenerating config field schema..."
	bash scripts/dev/run.sh --writable bash scripts/release/generate-schema.sh

## Repo Setup Targets
## Pre-commit composition: keel's schema-regen runs before the canonical
## pre-commit checks (format + lint + governance-gate). Pure prerequisite
## aggregator on the canonical `pre-commit` target — same pattern as `help`.
## install-hooks is inherited from canonical; it installs a hook that runs
## `make run-pre-commit`, so this prerequisite fires at commit time.
run-pre-commit: generate-schema-staged
generate-schema-staged:
	@echo "📐 Regenerating + staging config schema (keel-specific)..."
	bash scripts/hooks/schema-regen.sh

create-fresh-repo: install-hooks
	bash scripts/create-fresh-repo.sh

## Branch protection as code (docs/governance.md): check is read-only; apply needs an admin GH_TOKEN.
check-rulesets: | ensure-container
	@echo "🛡️  Checking rulesets against .github/rulesets..."
	bash scripts/dev/run.sh bash scripts/admin/apply-rulesets.sh check

apply-rulesets: | ensure-container
	@echo "🛡️  Applying .github/rulesets..."
	bash scripts/dev/run.sh bash scripts/admin/apply-rulesets.sh apply

## Universal Canonical Targets (language-independent interface; mandated by CI standards)
build: build-min

test-integration: test-integrity test-consistency

setup-pebble: | ensure-container
	@echo "🔧 Installing pebble..."
	bash scripts/dev/run.sh bash scripts/ci/setup-pebble.sh

setup-kind: | ensure-container
	@echo "🔧 Installing kind..."
	bash scripts/dev/run.sh bash scripts/ci/setup-kind.sh

## CI Gate Targets
test-smoke-windows:
	@echo "🪟 Running Windows smoke test..."
	./scripts/build/test-smoke-windows.sh

## BATS Runner Targets (artifact-based; distinct from test-integrity which builds first)
test-integrity-artifact: | ensure-container
	@echo "🧪 Running BATS integrity suite against downloaded artifact..."
	bash scripts/dev/run.sh bash scripts/test/test-integrity-artifact.sh

test-example-artifact: | ensure-container
	@echo "🧪 Running BATS example suite..."
	bash scripts/dev/run.sh bash scripts/test/test-example-artifact.sh

build-example: | ensure-container
	@echo "🔨 Building examples/myapp (CI, no BATS)..."
	bash scripts/dev/run.sh bash scripts/build/build-example.sh

## build-release-binaries: every flavor for every architecture, concurrently, under its release name.
build-release-binaries: | ensure-container
	@echo "⚓ Building release binaries..."
	bash scripts/dev/run.sh bash scripts/release/build-binaries.sh

## Kubernetes Helper Targets (internal; called by test-k8s)
test-k8s-cluster:
	@echo "⎈ Running k8s cluster tests..."
	./scripts/k8s/test-k8s-cluster.sh

deploy-k8s-helm:
	@echo "⎈ Deploying Helm chart to kind cluster..."
	./scripts/k8s/deploy-k8s-helm.sh

deploy-k8s-stack:
	@echo "⎈ Deploying the compose-equivalent k8s stack..."
	./scripts/k8s/deploy-k8s-stack.sh up

teardown-k8s-stack:
	@echo "🧹 Removing the compose-equivalent k8s stack..."
	./scripts/k8s/deploy-k8s-stack.sh down

load-k8s-kind:
	@echo "⎈ Loading image into kind cluster..."
	./scripts/k8s/load-k8s-kind.sh

setup-k8s-kind:
	@echo "⎈ Setting up kind cluster..."
	./scripts/k8s/setup-k8s-kind.sh

teardown-k8s-kind:
	@echo "⎈ Tearing down kind cluster..."
	./scripts/k8s/teardown-k8s-kind.sh

setup-ghcr:
	@echo "🔧 Setting up GHCR credentials..."
	./scripts/release/setup-ghcr.sh

## Additional Release Targets
run-release-ci:
	@echo "🚀 Running release CI pipeline..."
	./scripts/release/run-release-ci.sh

## Additional Utility Targets
report-size: | ensure-container
	@echo "📏 Generating binary size report..."
	bash scripts/dev/run.sh bash scripts/build/report-size.sh

create-roadmap-issues:
	@echo "🗺️  Syncing roadmap issues..."
	./scripts/create-roadmap-issues.sh

## Utility Targets
# Host uid:gid on Linux, where the dev image runs as root (set-dist-permissions hands dist/ back); empty elsewhere.
## keel's linters: also run first by canonical lint-all.sh (`make lint-all`, the commit hook), and runnable alone.
lint-all-local: | ensure-container
	@echo "🔍 Running keel's own linters..."
	bash scripts/dev/run.sh bash scripts/lint-all.local.sh

## Local artifact cleanup — also delegated to by canonical clean.sh (`make clean`)
clean-local:
	@echo "🧹 Removing keel build artifacts (dist/, target/)..."
	bash scripts/clean.local.sh

## Schema-diff bump hook (PREV_TAG=vX.Y.Z) — also called by canonical create-release.sh (`make create-release`)
create-release-local:
	@echo "📐 Computing bump level from the config schema diff..."
	bash scripts/release/create-release.local.sh $(PREV_TAG)

## The CA bundle image keel's images copy from: run by make publish-release-images, and runnable alone.
publish-release-images-local:
	@echo "🐳 Building the CA bundle image..."
	bash scripts/release/publish-release-images.local.sh

## keel's part of the canonical native targets: run by them, and runnable alone.
build-native-local: | ensure-container
	@echo "🔨 Building keel and pebble for $(OS)/$(ARCH)..."
	bash scripts/dev/run.sh bash scripts/test/build-native.local.sh $(OS) $(ARCH) target/native/$(OS)-$(ARCH) $(EXT)

test-native-integrity-local:
	@echo "🧪 Running the integrity suite against dist/..."
	bash scripts/test/test-native-integrity.local.sh

## keel's gosec: skips submodule folders (.standards first) and accepts reviewed sites (config/gosec-sites.local.txt).
lint-gosec-local: | ensure-container
	@echo "🔍 Running gosec on keel's own code..."
	bash scripts/dev/run.sh bash scripts/lint/lint-gosec.local.sh

## keel's unit-test layout: tests for {foo}.go in {foo}_test.go (#40); part of lint and the commit hook.
lint: lint-test-layout
lint-test-layout: | ensure-container
	@echo "🔍 Checking the unit-test layout..."
	bash scripts/dev/run.sh bash scripts/lint/lint-test-layout.local.sh

## keel's Helm schema audit: the chart's values follow the config schema (#41); its own CI job.
lint-helm-schema: | ensure-container
	@echo "🔍 Auditing helm/keel values against the config schema..."
	bash scripts/dev/run.sh bash scripts/lint/lint-helm-schema.local.sh

## Pin hook (WHAT=pins|holds) — also called by canonical update-pins.sh (`make update-pins`)
update-pins-local:
	@echo "📌 keel pin hook: $(WHAT)"
	bash scripts/release/update-pins.local.sh $(WHAT)

build-dev-image: | ensure-container
	@echo "🔨 Putting keel's dev image for this tree on this machine..."
	bash scripts/dev/build-image.local.sh

publish-dev-image-local: | ensure-container
	@echo "📦 keel publish hook: $(WHAT)"
	bash scripts/dev/publish.local.sh $(WHAT)

## report-coverage: pretty-print the merged per-file LCOV (from `make test-coverage`)
## as an ascending, colorized console table with coverage bars. Read-only view;
## does not run tests or alter the baseline.
report-coverage: | ensure-container
	bash scripts/dev/run.sh bash scripts/test/report-coverage.sh

## help composes keel's curated summary (help-local) with the canonical
## auto-generated target list. help-local is a pure prerequisite aggregator
## (no recipe on `help` itself), so Make runs help-local first, then the
## canonical `help` recipe — no recipe-override, and refresh-governance treats
## the aggregator as non-drifting.
help: help-local

help-local:
	@echo "Keel Build System"
	@echo "Usage: make [target]"
	@echo ""
	@echo "Build:"
	@echo "  build-min             Build the minimalist <3MB binary (Default)"
	@echo "  build-max             Build the FIPS-compliant static binary"
	@echo "  build-max-no-fips     Build the full-feature non-FIPS binary"
	@echo ""
	@echo "Test:"
	@echo "  test             Run unit + consistency + BATS integrity tests"
	@echo "  test-consistency Consistency suite only"
	@echo "  test-integrity   BATS integrity suite only"
	@echo "  test-compose     Docker Compose integration tests (P3)"
	@echo "  test-compose-keep  Same but leave compose stack running"
	@echo "  test-k8s         kind k8s integration tests (requires Docker)"
	@echo "  test-coverage         Generate coverage report (total %%, uncovered, total lines)"
	@echo ""
	@echo "Local k8s (Apple container / P3.5):"
	@echo "  setup-container           Start k8s cluster + keel-toolbox via Apple container"
	@echo "  deploy-container          Build keel:test + helm install to the cluster"
	@echo "  test-container            Run k8s integration tests"
	@echo "  test-container-stack      Run compose suite on the k8s stack (hostPorts, in-node)"
	@echo "  test-container-stack-keep Same but leave the stack running"
	@echo "  teardown-container        Uninstall + delete toolbox and cluster"
	@echo "  setup-container-host-network    Check/enforce the host<->guest bridge route"
	@echo ""
	@echo "Lint:"
	@echo "  lint            gofmt check + go vet + staticcheck"
	@echo "  lint-go         gofmt check + go vet + staticcheck"
	@echo ""
	@echo "Release:"
	@echo "  generate-sbom              Generate SBOM (requires syft)"
	@echo "  sign-release              Sign artifacts (requires cosign)"
	@echo "  build-release-binaries          Build every release binary, concurrently"
	@echo "  generate-schema                Regenerate pkg/config/schema.yaml from config.Config"
	@echo "  create-release            Compute and tag next semver release"
	@echo "  create-release FORCE=v1.0.0  Force a specific version"
	@echo "  create-release DRY_RUN=1  Compute and show the next tag; no tag, no push"
	@echo ""
	@echo "Setup (CI tooling):"
	@echo "  setup-bats        Install bats-core"
	@echo "  setup-pebble      Install pebble ACME test server"
	@echo "  setup-kind        Install kind (in the dev image)"
	@echo "  setup-markdownlint Install markdownlint-cli"
	@echo "  setup-staticcheck Install staticcheck linter"
	@echo "  setup-syft        Install syft SBOM tool"
	@echo "  setup-cosign      Install cosign signing tool"
	@echo ""
	@echo "CI Gates:"
	@echo "  check-pr-policy      Run PR policy check"
	@echo "  check-secrets    Run secret scan"
	@echo "  check-dco            Run DCO sign-off check"
	@echo "  check-coverage-delta Check coverage delta vs base branch"
	@echo "  test-smoke-windows  Smoke test the Windows binary (built by build-native)"
	@echo "  build-example  Build examples/myapp only (no BATS)"
	@echo "  test-integrity-artifact    Run BATS integrity suite (artifact must exist)"
	@echo "  test-example-artifact      Run BATS example suite"
	@echo ""
	@echo "Repo Setup:"
	@echo "  create-fresh-repo      First-time setup: download deps + install hooks"
	@echo "  install-hooks   Install git hooks from scripts/hooks/"
	@echo ""
	@echo "Utilities:"
	@echo "  audit             Run CI/Makefile standards compliance audit"
	@echo "  generate-certs         Generate self-signed TLS certs for testing"
	@echo "  check-legal-drift Check legal file drift"
	@echo "  report-size       Generate binary size report"
	@echo "  create-roadmap-issues    Sync roadmap issues"
	@echo "  lint-newlines     Check trailing newlines"
	@echo "  clean             Remove build artifacts"
	@echo ""
	@echo "Universal targets (canonical; language-independent):"
	@echo "  build             Alias for build-min (default artifact)"
	@echo "  test-integration  Runs test-integrity and test-consistency"
