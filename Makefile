# Keel Master Orchestrator
# Part of the KeelCore governance: Ripped. Hard. Shredded.

.PHONY: all clean min max max-no-fips test test-integrity help

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
	@echo "🧪 Running Shredded Unit Tests..."
	go test -v -race ./pkg/...

test-integrity:
	@echo "🧪 Running BATS Integrity Suite..."
	./scripts/build/ci_test_binary.sh

## Utility Targets
clean:
	@echo "🧹 Cleaning dist/ and build artifacts..."
	rm -rf dist/
	go clean

help:
	@echo "Keel Build System"
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  min           Build the minimalist <3MB binary (Default)"
	@echo "  max           Build the FIPS-compliant static binary"
	@echo "  max-no-fips   Build the full-feature non-FIPS binary"
	@echo "  test          Run unit and BATS integrity tests"
	@echo "  clean         Remove build artifacts"
