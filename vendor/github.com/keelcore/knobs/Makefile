# Consumer Makefile — delegates canonical targets to the canonical Makefile
# shipped in the .standards submodule.
#
# All universal targets (build, test, lint, audit, check-*, setup-*,
# test-coverage-*, install-hooks, format, refresh-governance, migrate-makefile,
# help, etc.) live in .standards/templates/Makefile.canonical, which this
# file includes. Project-specific build/test/clean behavior is customized
# by creating scripts/<target>.local.sh files — the canonical delegate
# scripts (scripts/build.sh etc.) pick those up automatically.
#
# Path resolution: when Make `include`s a file, it does NOT change CWD.
# Recipe lines in Makefile.canonical (e.g. `bash scripts/lint/lint-bash.sh`)
# resolve relative to THIS Makefile's directory (the consumer repo root),
# which means they run the consumer's copies of the scripts — as intended.

# Existence guard: fail loudly if the submodule isn't initialized, since
# every canonical target would otherwise be reported as "no rule to make
# target" with no hint why.
ifeq (,$(wildcard .standards/templates/Makefile.canonical))
$(error .standards/templates/Makefile.canonical not found. Run: git submodule update --init --recursive)
endif

include .standards/templates/Makefile.canonical

# Entry points (cmd/*/main.go) are exercised through the built harness (tests/conformance.bats), not unit tests:
# outside the per-file coverage gate. Paths are module-relative, so cmd/ at the root matches too.
export GO_COVERAGE_IGNORE_REGEX := (^|/)cmd/

## Project-specific additions go below. Override canonical targets only
## via scripts/<target>.local.sh — do not redefine canonical targets here
## (Make will warn about overriding recipes).

.PHONY: create-release-local
.PHONY: check-rulesets
.PHONY: apply-rulesets

## CHANGELOG bump hook (PREV_TAG=vX.Y.Z) — also called by canonical create-release.sh (`make create-release`)
create-release-local:
	@echo "📐 Computing bump level from the CHANGELOG entries since the previous tag..."
	bash scripts/release/create-release.local.sh $(PREV_TAG)

## Branch protection as code (docs/governance.md): check is read-only; apply needs an admin GH_TOKEN.
check-rulesets:
	@echo "🛡️  Checking rulesets against .github/rulesets..."
	bash scripts/dev/run.sh bash scripts/admin/apply-rulesets.sh check

apply-rulesets:
	@echo "🛡️  Applying .github/rulesets..."
	bash scripts/dev/run.sh bash scripts/admin/apply-rulesets.sh apply

.PHONY: build-conform

## Every implementation's conformance harness, into target/conform/ (IMPL=go builds one)
build-conform:
	@echo "🔨 Building conformance harnesses..."
	bash scripts/dev/run.sh bash scripts/build/build-conform.sh $(IMPL)

.PHONY: lint-test-layout-local

lint: lint-test-layout-local

## Unit-test layout: all unit tests for {foo}.go in {foo}_test.go (knobs#13) — also run by `make lint`
lint-test-layout-local: | ensure-container
	@echo "🔍 Checking the unit-test layout..."
	bash scripts/dev/run.sh bash scripts/lint/lint-test-layout.local.sh

.PHONY: check-schema-types

## The type-match audit: the Go bindings' type table against the test schemas (after build-conform)
check-schema-types:
	@echo "🔍 Checking binding types against the schemas..."
	bash scripts/dev/run.sh bash scripts/test/check-schema-types.sh

.PHONY: test-integration-local

## knobs' integration suites (conformance per implementation, then the other BATS suites); also
## delegated to by canonical test-integration.sh (`make test-integration`)
test-integration-local:
	@echo "🧪 Running knobs integration suites..."
	bash scripts/dev/run.sh bash scripts/test-integration.local.sh

.PHONY: publish-go-module

## Publish the Go module at the release tag to the checksum database and proxy (release.yml)
publish-go-module:
	@echo "📦 Publishing the Go module..."
	bash scripts/dev/run.sh bash scripts/release/publish-go-module.sh

.PHONY: pull-release-module

## The release's module zip and SHA256SUMS into dist/, checked (release.yml attests the zip)
pull-release-module:
	@echo "⬇️  Fetching the release's module zip..."
	bash scripts/dev/run.sh bash scripts/release/pull-release-module.sh

.PHONY: test-manual-release

## CI-only (ci.yml, workflow_dispatch from main): in a new repository, a manual release's read-only steps take
## the tag from INPUT_TAG, never the branch. Runs on the host: it calls make, and make calls the tool image.
test-manual-release: | ensure-container
	@echo "🧪 Checking a manual release's tag in a new repository..."
	bash scripts/test/test-manual-release.sh

.PHONY: build-native-scratch
.PHONY: test-native-scratch

## CI-only (ci.yml, workflow_dispatch): make build-native OS ARCH in a new repository from this commit, its
## tests.tsv checked, the build copied to target/native/. Runs on the host: it calls make.
build-native-scratch: | ensure-container
	@echo "🔨 Native build in a new repository..."
	bash scripts/test/build-native-scratch.sh $(OS) $(ARCH)

## CI-only, on a native runner: make test-native and test-native-integrity DIR in a new repository from this commit
test-native-scratch:
	@echo "🧪 Native tests in a new repository..."
	bash scripts/test/test-native-scratch.sh $(DIR)

.PHONY: build-native-local
.PHONY: test-native-integrity-local

## knobs' part of the canonical native targets: run by them, and runnable alone.
build-native-local:
	@echo "🔨 Building knobs-conform for $(OS)/$(ARCH)..."
	bash scripts/dev/run.sh bash scripts/test/build-native.local.sh $(OS) $(ARCH) target/native/$(OS)-$(ARCH) $(EXT)

test-native-integrity-local:
	@echo "🧪 Running the conformance and integrity suites against the native harness..."
	bash scripts/test/test-native-integrity.local.sh
