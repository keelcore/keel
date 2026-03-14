# Keel Master Orchestrator
# Part of the KeelCore governance: Ripped. Hard. Shredded.

.PHONY: all clean min max max-no-fips \
        build integration-test unit-test \
        test test-unit test-consistency test-integrity test-compose test-k8s coverage \
        lint lint-go lint-helm lint-helm-validate lint-fmt lint-newlines lint-md \
        release-checksums release-checksums-verify release-sbom release-sign release-upload \
        release-rename release-docker release-helm-push release-ci \
        colima-setup colima-deploy colima-test colima-teardown \
        gen-certs gen-schema create-release install-hooks fresh-repo help \
        setup-helm setup-bats setup-pebble setup-kind setup-staticcheck setup-syft setup-cosign setup-markdownlint \
        setup-docker-macos setup-kubeconform setup-ghcr \
        ci-pr-policy ci-secret-scan ci-dco ci-coverage-delta ci-smoke-windows \
        bats-integrity bats-example ci-build-example dist-chmod \
        audit check-legal-drift size-report roadmap-issues \
        k8s-cluster-test k8s-helm-deploy k8s-kind-load k8s-kind-setup k8s-kind-teardown

# Default target: build the shredded minimalist binary
all: min

## Build Targets
min:
	@echo "⚓ Building Shredded (BYOS)..."
	./scripts/build/ci_min.sh

max:
	@echo "⚓ Building Rock-Hard (FIPS/Corporate)..."
	./scripts/build/ci_max.sh

max-no-fips:
	@echo "⚓ Building Ripped (Full Feature/No-FIPS)..."
	./scripts/build/ci_max_no_fips.sh

## Lint Targets
lint: lint-fmt lint-go lint-newlines lint-md

lint-fmt:
	@echo "🔍 Running gofmt check..."
	./scripts/lint/fmt.sh

lint-go:
	@echo "🔍 Running Go lint..."
	./scripts/lint/go.sh

lint-md:
	@echo "🔍 Running markdown lint..."
	./scripts/lint/md.sh

lint-helm:
	@echo "🔍 Running Helm lint..."
	./scripts/lint/helm.sh

lint-helm-validate:
	@echo "🔍 Validating Helm template against Kubernetes schema..."
	./scripts/lint/helm-validate.sh

## Release Targets
release-checksums:
	@echo "🔐 Generating SHA256SUMS..."
	./scripts/release/checksums.sh

release-sbom:
	@echo "📋 Generating SBOM..."
	./scripts/release/sbom.sh

release-sign:
	@echo "✍️  Signing artifacts..."
	./scripts/release/sign.sh

release-upload:
	@echo "🚀 Uploading release artifacts..."
	RELEASE_TAG=$(TAG) ./scripts/release/upload.sh

## Testing Targets
test: test-unit test-consistency test-integrity lint-helm lint-helm-validate

test-unit:
	@echo "🧪 Running unit tests..."
	./scripts/test/ci.sh

coverage:
	@echo "📊 Generating coverage report..."
	./scripts/test/coverage.sh

test-consistency:
	@echo "🧪 Running consistency suite..."
	./scripts/test/consistency.sh

test-integrity:
	@echo "🧪 Running BATS integrity suite..."
	./scripts/build/ci_test_binary.sh

test-example:
	@echo "🧪 Running examples/myapp tests..."
	./scripts/build/ci_example.sh
	bats examples/myapp/myapp.bats

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

## P3.5 Local k8s (Colima) Targets
colima-setup:
	@echo "☸️  Starting local k8s cluster via Colima..."
	./scripts/colima/setup.sh

colima-deploy:
	@echo "⎈ Building and deploying Keel to Colima k8s..."
	./scripts/colima/deploy.sh

colima-test:
	@echo "🧪 Running k8s integration tests against Colima cluster..."
	./scripts/colima/test.sh

colima-teardown:
	@echo "🧹 Tearing down Colima k8s environment..."
	./scripts/colima/teardown.sh

## Certificate Generation
gen-certs:
	@echo "🔑 Generating self-signed test certificates..."
	./tests/fixtures/gen-certs.sh

gen-schema:
	@echo "📐 Regenerating config field schema..."
	./scripts/release/gen-schema.sh

create-release:
	@echo "🏷️  Creating release tag..."
	./scripts/release/create-release.sh $(if $(FORCE),--force $(FORCE),)

## Repo Setup Targets
install-hooks:
	@echo "🪝 Installing git hooks..."
	ln -sf "$(CURDIR)/scripts/hooks/pre-commit" .git/hooks/pre-commit
	@echo "✅ Hooks installed"

fresh-repo: install-hooks
	@echo "📦 Installing Go tools..."
	go mod download
	@echo "✅ Repo ready"

## Universal Canonical Targets (language-independent interface; mandated by CI standards)
build: min

unit-test: test-unit

integration-test: test-integrity

## Audit Target
audit:
	@echo "🔍 Running CI/Makefile audit..."
	./scripts/ci/audit-make-targets.sh

## Setup Targets
setup-helm:
	@echo "🔧 Installing Helm..."
	./scripts/ci/setup-helm.sh

setup-bats:
	@echo "🔧 Installing bats-core..."
	./scripts/ci/setup-bats.sh

setup-pebble:
	@echo "🔧 Installing pebble..."
	./scripts/ci/setup-pebble.sh

setup-kind:
	@echo "🔧 Installing kind and creating cluster..."
	./scripts/ci/setup-kind.sh

setup-markdownlint:
	@echo "🔧 Installing markdownlint-cli..."
	./scripts/ci/setup-markdownlint.sh

setup-staticcheck:
	@echo "🔧 Installing staticcheck..."
	go install honnef.co/go/tools/cmd/staticcheck@latest

setup-syft:
	@echo "🔧 Installing syft..."
	./scripts/release/install-syft.sh

setup-cosign:
	@echo "🔧 Installing cosign..."
	./scripts/release/install-cosign.sh

## CI Gate Targets
ci-pr-policy:
	@echo "🔍 Running PR policy check..."
	./scripts/ci/pr-policy.sh

ci-secret-scan:
	@echo "🔍 Running secret scan..."
	./scripts/ci/secret-scan.sh

ci-dco:
	@echo "🔍 Running DCO sign-off check..."
	./scripts/ci/dco-check.sh

ci-coverage-delta:
	@echo "📊 Checking coverage delta..."
	./scripts/test/coverage-delta.sh

ci-smoke-windows:
	@echo "🪟 Running Windows smoke test..."
	./scripts/build/ci_smoke_windows.sh

## BATS Runner Targets (artifact-based; distinct from test-integrity which builds first)
bats-integrity:
	@echo "🧪 Running BATS integrity suite against downloaded artifact..."
	bats tests/integrity.bats

bats-example:
	@echo "🧪 Running BATS example suite..."
	bats examples/myapp/myapp.bats

ci-build-example:
	@echo "🔨 Building examples/myapp (CI, no BATS)..."
	./scripts/build/ci_example.sh

## Additional Release Targets
release-checksums-verify:
	@echo "🔐 Verifying SHA256SUMS..."
	./scripts/release/checksums.sh --verify

release-rename:
	@echo "📦 Renaming release artifacts with platform suffix..."
	./scripts/release/rename-assets.sh

release-docker:
	@echo "🐳 Building and pushing container images..."
	./scripts/release/docker.sh

release-helm-push:
	@echo "⎈ Packaging and pushing Helm chart..."
	./scripts/release/helm-push.sh

## Kubernetes Helper Targets (internal; called by test-k8s)
k8s-cluster-test:
	@echo "⎈ Running k8s cluster tests..."
	./scripts/k8s/cluster-test.sh

k8s-helm-deploy:
	@echo "⎈ Deploying Helm chart to kind cluster..."
	./scripts/k8s/helm-deploy.sh

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
setup-docker-macos:
	@echo "🔧 Setting up Docker on macOS runner..."
	./scripts/ci/setup-docker-macos.sh

setup-kubeconform:
	@echo "🔧 Installing kubeconform..."
	./scripts/ci/setup-kubeconform.sh

setup-ghcr:
	@echo "🔧 Setting up GHCR credentials..."
	./scripts/release/setup-ghcr.sh

## Additional Lint Targets
lint-newlines:
	@echo "🔍 Checking trailing newlines..."
	./scripts/lint/newlines.sh

## Additional Release Targets
release-ci:
	@echo "🚀 Running release CI pipeline..."
	./scripts/release/ci.sh

## Additional Utility Targets
check-legal-drift:
	@echo "⚖️  Checking legal file drift..."
	./scripts/check-legal-drift.sh

size-report:
	@echo "📏 Generating binary size report..."
	./scripts/build/size-report.sh

roadmap-issues:
	@echo "🗺️  Syncing roadmap issues..."
	./scripts/roadmap-issues.sh

## Utility Targets
dist-chmod:
	@echo "🔑 Making dist binaries executable..."
	chmod +x dist/keel-*

clean:
	@echo "🧹 Cleaning dist/ and build artifacts..."
	rm -rf dist/
	go clean

help:
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
	@echo "  test-integrity   BATS integrity suite only"
	@echo "  test-compose     Docker Compose integration tests (P3)"
	@echo "  test-compose-keep  Same but leave compose stack running"
	@echo "  test-k8s         kind k8s integration tests (requires Docker)"
	@echo "  coverage         Generate coverage report (total %%, uncovered, total lines)"
	@echo ""
	@echo "Local k8s (Colima / P3.5):"
	@echo "  colima-setup    Install deps + start local k8s via Colima"
	@echo "  colima-deploy   Build keel:test + helm install to Colima"
	@echo "  colima-test     Run k8s integration tests"
	@echo "  colima-teardown Uninstall + stop Colima cluster"
	@echo ""
	@echo "Lint:"
	@echo "  lint            gofmt check + go vet + staticcheck"
	@echo "  lint-fmt        gofmt -s check only"
	@echo "  lint-go         go vet + staticcheck only"
	@echo "  lint-md         markdownlint-cli check on all tracked .md files"
	@echo "  lint-helm       Helm chart lint (no cluster required)"
	@echo "  lint-helm-validate  Helm template schema validation via kubeconform (no cluster required)"
	@echo ""
	@echo "Release:"
	@echo "  release-checksums         Generate dist/SHA256SUMS"
	@echo "  release-checksums-verify  Verify dist/SHA256SUMS"
	@echo "  release-sbom              Generate SBOM (requires syft)"
	@echo "  release-sign              Sign artifacts (requires cosign)"
	@echo "  release-rename            Rename artifacts with platform suffix"
	@echo "  release-docker            Build and push container images"
	@echo "  release-helm-push         Package and push Helm chart"
	@echo "  release-upload TAG=v1.x.x  Upload to GitHub Release (sets RELEASE_TAG)"
	@echo "  gen-schema                Regenerate pkg/config/schema.yaml from config.Config"
	@echo "  create-release            Compute and tag next semver release"
	@echo "  create-release FORCE=v1.0.0  Force a specific version"
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
	@echo ""
	@echo "CI Gates:"
	@echo "  ci-pr-policy      Run PR policy check"
	@echo "  ci-secret-scan    Run secret scan"
	@echo "  ci-dco            Run DCO sign-off check"
	@echo "  ci-coverage-delta Check coverage delta vs base branch"
	@echo "  ci-smoke-windows  Build and smoke test Windows binary"
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