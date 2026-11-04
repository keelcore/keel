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
.PHONY: test-unit
.PHONY: test-consistency
.PHONY: test-integrity
.PHONY: test-compose
.PHONY: test-k8s
.PHONY: coverage
.PHONY: lint-go
.PHONY: lint-helm
.PHONY: lint-helm-validate
.PHONY: lint-fmt
.PHONY: lint-newlines
.PHONY: lint-md
.PHONY: lint-vuln
.PHONY: lint-gosec
.PHONY: test-fuzz
.PHONY: lint-workflows
.PHONY: lint-helm-policy
.PHONY: lint-licenses
.PHONY: release-vex
.PHONY: lint-vex
.PHONY: release-checksums
.PHONY: release-checksums-verify
.PHONY: release-sbom
.PHONY: release-sign
.PHONY: release-upload
.PHONY: release-rename
.PHONY: release-docker
.PHONY: release-helm-push
.PHONY: release-cosign
.PHONY: release-verify
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
.PHONY: help
.PHONY: help-local
.PHONY: pre-commit
.PHONY: keel-schema-regen
.PHONY: setup-helm
.PHONY: setup-bats
.PHONY: setup-pebble
.PHONY: setup-kind
.PHONY: setup-syft
.PHONY: setup-gotestsum
.PHONY: setup-grype
.PHONY: setup-govulncheck
.PHONY: setup-gosec
.PHONY: setup-actionlint
.PHONY: setup-go-licenses
.PHONY: setup-zizmor
.PHONY: setup-kubescape
.PHONY: setup-markdownlint
.PHONY: setup-kubeconform
.PHONY: setup-ghcr
.PHONY: ci-pr-policy
.PHONY: ci-secret-scan
.PHONY: ci-dco
.PHONY: ci-coverage-delta
.PHONY: ci-smoke-windows
.PHONY: native-build
.PHONY: native-run
.PHONY: native-integrity
.PHONY: bats-integrity
.PHONY: bats-example
.PHONY: ci-build-example
.PHONY: dist-chmod
.PHONY: clean-local
.PHONY: setup-bats-local
.PHONY: create-release-local
.PHONY: update-pins-local
.PHONY: dev-image-publish-local
.PHONY: coverage-profile
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

lint-fmt:
	@echo "🔍 Running gofmt check..."
	bash scripts/dev/run.sh bash scripts/lint/fmt.sh

lint-md:
	@echo "🔍 Running markdown lint..."
	bash scripts/dev/run.sh bash scripts/lint/md.sh

lint-helm:
	@echo "🔍 Running Helm lint..."
	bash scripts/dev/run.sh bash scripts/lint/helm.sh

lint-helm-validate:
	@echo "🔍 Validating Helm template against Kubernetes schema..."
	bash scripts/dev/run.sh bash scripts/lint/helm-validate.sh

lint-vuln:
	@echo "🔍 Running govulncheck..."
	bash scripts/dev/run.sh bash scripts/lint/vuln.sh

lint-gosec:
	@echo "🔍 Running gosec..."
	bash scripts/dev/run.sh bash scripts/lint/gosec.sh

test-fuzz:
	@echo "🧪 Running fuzz targets..."
	bash scripts/dev/run.sh bash scripts/test/fuzz.sh

lint-workflows:
	@echo "🔍 Linting workflows (actionlint, zizmor)..."
	bash scripts/dev/run.sh bash scripts/lint/workflows.sh

lint-helm-policy:
	@echo "🔍 Scanning chart policy (kubescape)..."
	bash scripts/dev/run.sh bash scripts/lint/helm-policy.sh

lint-licenses:
	@echo "🔍 Checking dependency licenses..."
	bash scripts/dev/run.sh bash scripts/lint/licenses.sh

## Release Targets
release-checksums:
	@echo "🔐 Generating SHA256SUMS..."
	bash scripts/dev/run.sh bash scripts/release/checksums.sh

release-upload:
	@echo "🚀 Uploading release artifacts..."
	RELEASE_TAG=$(TAG) ./scripts/release/upload.sh

test-unit:
	@echo "🧪 Running unit tests..."
	bash scripts/dev/run.sh bash scripts/test/ci.sh

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

## Universal Canonical Targets (language-independent interface; mandated by CI standards)
build: min

unit-test: test-unit

integration-test: test-integrity

## Setup Targets
setup-helm:
	@echo "🔧 Installing Helm..."
	./scripts/ci/setup-helm.sh

setup-pebble:
	@echo "🔧 Installing pebble..."
	bash scripts/dev/run.sh bash scripts/ci/setup-pebble.sh

setup-kind:
	@echo "🔧 Installing kind and creating cluster..."
	./scripts/ci/setup-kind.sh

setup-gotestsum:
	@echo "🔧 Installing gotestsum..."
	bash scripts/dev/run.sh bash scripts/ci/setup-gotestsum.sh

setup-grype:
	@echo "🔧 Installing grype..."
	bash scripts/dev/run.sh bash scripts/ci/setup-grype.sh

setup-govulncheck:
	@echo "🔧 Installing govulncheck..."
	bash scripts/dev/run.sh bash scripts/ci/setup-govulncheck.sh

setup-gosec:
	@echo "🔧 Installing gosec..."
	bash scripts/dev/run.sh bash scripts/ci/setup-gosec.sh

setup-actionlint:
	@echo "🔧 Installing actionlint..."
	bash scripts/dev/run.sh bash scripts/ci/setup-actionlint.sh

setup-go-licenses:
	@echo "🔧 Installing go-licenses..."
	bash scripts/dev/run.sh bash scripts/ci/setup-go-licenses.sh

setup-zizmor:
	@echo "🔧 Installing zizmor..."
	bash scripts/dev/run.sh bash scripts/ci/setup-zizmor.sh

setup-kubescape:
	@echo "🔧 Installing kubescape..."
	bash scripts/dev/run.sh bash scripts/ci/setup-kubescape.sh

## CI Gate Targets
ci-smoke-windows:
	@echo "🪟 Running Windows smoke test..."
	./scripts/build/ci_smoke_windows.sh

## Native platforms: the dev image cross-builds the test binaries, keel, pebble and bats for
## OS/ARCH (target/native/<os>-<arch>, dist/); the native macOS or Windows host only runs them.
native-build:
	@echo "🔨 Cross-building native test binaries for $(OS)/$(ARCH)..."
	bash scripts/dev/run.sh bash scripts/test/native-build.sh $(OS) $(ARCH)

native-run:
	@echo "🧪 Running native test binaries ($(DIR))..."
	bash scripts/test/native-run.sh $(DIR)

native-integrity:
	@echo "🧪 Running the integrity suite natively ($(DIR))..."
	bash scripts/test/native-integrity.sh $(DIR)

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

## Additional Release Targets
release-checksums-verify:
	@echo "🔐 Verifying SHA256SUMS..."
	bash scripts/dev/run.sh bash scripts/release/checksums.sh --verify

release-rename:
	@echo "📦 Renaming release artifacts with platform suffix..."
	bash scripts/dev/run.sh bash scripts/release/rename-assets.sh

release-docker:
	@echo "🐳 Building and pushing container images..."
	./scripts/release/docker.sh

release-helm-push:
	@echo "⎈ Packaging and pushing Helm chart..."
	bash scripts/dev/run.sh bash scripts/release/helm-push.sh

release-cosign:
	@echo "🔏 cosign (GHCR, dev image) $(ARGS)..."
	bash scripts/dev/run.sh bash scripts/release/cosign.sh $(ARGS)

lint-vex:
	@echo "🔍 Validating generated OpenVEX document..."
	bash scripts/dev/run.sh bash scripts/lint/vex.sh

release-vex:
	@echo "🧾 Generating OpenVEX (govulncheck)..."
	bash scripts/dev/run.sh bash scripts/release/vex.sh $(ARGS)

release-verify:
	@echo "🔍 Verifying published release..."
	bash scripts/release/verify-release.sh

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

## Additional Setup Targets
setup-kubeconform:
	@echo "🔧 Installing kubeconform..."
	bash scripts/dev/run.sh bash scripts/ci/setup-kubeconform.sh

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
DIST_OWNER := $(shell [ "$$(uname -s)" = Linux ] && printf '%s:%s' "$$(id -u)" "$$(id -g)")

dist-chmod:
	@echo "🔑 Making dist binaries executable..."
	DIST_OWNER='$(DIST_OWNER)' KEELCORE_DEV_ENV=DIST_OWNER bash scripts/dev/run.sh bash scripts/build/dist-chmod.sh

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

## Pin hook (WHAT=pins|holds) — also called by canonical update-pins.sh (`make update-pins`)
update-pins-local:
	@echo "📌 keel pin hook: $(WHAT)"
	bash scripts/release/update-pins.local.sh $(WHAT)

dev-image-publish-local:
	@echo "📦 keel publish hook: $(WHAT)"
	bash scripts/dev/publish.local.sh $(WHAT)

## coverage-profile: write the whole-module Go statement-coverage profile to
## target/coverage.txt (with a total%/uncovered/lines summary). This is the profile
## ci.sh generates during `make test-unit` and uploads to Codecov — named for
## the artifact it produces, and distinct from the canonical per-file LCOV
## flow (coverage-go / coverage-aggregate / ci-coverage-no-regression).
coverage-profile:
	@echo "📊 Generating Go coverage profile (target/coverage.txt)..."
	bash scripts/dev/run.sh bash scripts/test/coverage.sh

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
	@echo "  test-unit        Unit tests only"
	@echo "  test-consistency Consistency suite only"
	@echo "  test-fuzz        Short fuzz pass over every Go fuzz target (FUZZTIME=10s)"
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
	@echo "  lint-fmt        gofmt -s check only"
	@echo "  lint-go         gofmt check + go vet + staticcheck"
	@echo "  lint-md         markdownlint-cli check on all tracked .md files"
	@echo "  lint-helm       Helm chart lint (no cluster required)"
	@echo "  lint-helm-validate  Helm template schema validation via kubeconform (no cluster required)"
	@echo "  lint-vuln         Dependency vulnerability gate (govulncheck)"
	@echo "  lint-gosec        Go security linter (gosec)"
	@echo "  lint-workflows    Workflow security lint (actionlint, zizmor)"
	@echo "  lint-helm-policy  Chart policy scan (kubescape)"
	@echo "  lint-licenses     Dependency license allowlist (go-licenses)"
	@echo ""
	@echo "Release:"
	@echo "  release-checksums         Generate dist/SHA256SUMS"
	@echo "  release-checksums-verify  Verify dist/SHA256SUMS"
	@echo "  release-sbom              Generate SBOM (requires syft)"
	@echo "  release-sign              Sign artifacts (requires cosign)"
	@echo "  release-rename            Rename artifacts with platform suffix"
	@echo "  release-docker            Build and push container images"
	@echo "  release-vex               Generate dist/keel.openvex.json from govulncheck"
	@echo "  lint-vex                  Validate dist/keel.openvex.json before it is attested"
	@echo "  release-verify            Verify signatures and attestations of a published release"
	@echo "  release-helm-push         Package and push Helm chart"
	@echo "  release-upload TAG=v1.x.x  Upload to GitHub Release (sets RELEASE_TAG)"
	@echo "  gen-schema                Regenerate pkg/config/schema.yaml from config.Config"
	@echo "  create-release            Compute and tag next semver release"
	@echo "  create-release FORCE=v1.0.0  Force a specific version"
	@echo "  create-release DRY_RUN=1  Compute and show the next tag; no tag, no push"
	@echo ""
	@echo "Setup (CI tooling):"
	@echo "  setup-helm        Install Helm"
	@echo "  setup-bats        Install bats-core"
	@echo "  setup-pebble      Install pebble ACME test server"
	@echo "  setup-kind        Install kind and create cluster"
	@echo "  setup-markdownlint Install markdownlint-cli"
	@echo "  setup-staticcheck Install staticcheck linter"
	@echo "  setup-syft        Install syft SBOM tool"
	@echo "  setup-cosign      Install cosign signing tool"
	@echo "  setup-grype       Install grype image scanner"
	@echo "  setup-govulncheck Install govulncheck"
	@echo "  setup-gosec       Install gosec"
	@echo "  setup-actionlint  Install actionlint"
	@echo "  setup-go-licenses Install go-licenses"
	@echo "  setup-zizmor      Install zizmor"
	@echo "  setup-kubescape   Install kubescape"
	@echo ""
	@echo "CI Gates:"
	@echo "  ci-pr-policy      Run PR policy check"
	@echo "  ci-secret-scan    Run secret scan"
	@echo "  ci-dco            Run DCO sign-off check"
	@echo "  ci-coverage-delta Check coverage delta vs base branch"
	@echo "  ci-smoke-windows  Smoke test the Windows binary (built by native-build)"
	@echo "  native-build      Cross-build native tests/binaries (OS=darwin|windows ARCH=arm64|amd64)"
	@echo "  native-run        Run cross-built native tests (DIR=target/native/<os>-<arch>)"
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
	@echo "  dist-chmod        Make dist/keel-* binaries executable"
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
