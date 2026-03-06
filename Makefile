# Keel Master Orchestrator
# Part of the KeelCore governance: Ripped. Hard. Shredded.

.PHONY: all clean min max max-no-fips \
        test test-unit test-integrity test-compose \
        colima-setup colima-deploy colima-test colima-teardown \
        gen-certs help

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

## Testing Targets
test: test-unit test-integrity

test-unit:
	@echo "🧪 Running unit tests..."
	./scripts/test/ci.sh

test-integrity:
	@echo "🧪 Running BATS integrity suite..."
	./scripts/build/ci_test_binary.sh

test-compose:
	@echo "🐳 Running Docker Compose integration tests (P3)..."
	./scripts/test/compose.sh

test-compose-keep:
	@echo "🐳 Running Docker Compose integration tests (stack stays up)..."
	./scripts/test/compose.sh --keep-up

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

## Utility Targets
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
	@echo "  test            Run unit + BATS integrity tests"
	@echo "  test-unit       Unit tests only"
	@echo "  test-integrity  BATS integrity suite only"
	@echo "  test-compose    Docker Compose integration tests (P3)"
	@echo "  test-compose-keep  Same but leave compose stack running"
	@echo ""
	@echo "Local k8s (Colima / P3.5):"
	@echo "  colima-setup    Install deps + start local k8s via Colima"
	@echo "  colima-deploy   Build keel:test + helm install to Colima"
	@echo "  colima-test     Run k8s integration tests"
	@echo "  colima-teardown Uninstall + stop Colima cluster"
	@echo ""
	@echo "Utilities:"
	@echo "  gen-certs       Generate self-signed TLS certs for testing"
	@echo "  clean           Remove build artifacts"