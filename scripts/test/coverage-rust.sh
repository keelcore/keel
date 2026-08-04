#!/usr/bin/env bash
# coverage-rust.sh
# Generates a Rust coverage report via cargo-llvm-cov: per-file LCOV at
# target/llvm-cov/lcov.info plus an HTML browse tree at target/llvm-cov/html/.
# cargo-llvm-cov runs the cargo test suite as a side effect, so this script
# also satisfies the "tests must pass" obligation for the Rust scope.
# Skips with exit 0 when no Rust scope is present (no Cargo.toml at
# REPO_ROOT) — invoked unconditionally by `make coverage` via the
# aggregator pattern; the per-language gate lives here. HALTs (non-zero) if
# Rust scope is detected but cargo-llvm-cov is not installed.

# bash configuration:
# 1) Exit script if you try to use an uninitialized variable.
set -o nounset

# 2) Exit script if a statement returns a non-true return value.
set -o errexit

# 3) Use the error status of the first failure, rather than that of the last item in a pipeline.
set -o pipefail

declare -r OUT_DIR='target/llvm-cov'
declare -r LCOV_PATH="${OUT_DIR}/lcov.info"
# Shared coverage bucket where each language drops its LCOV for the aggregate.
declare -r COVERAGE_DIR="${COVERAGE_DIR:-target/coverage}"

function main() {
  exec 5>&1
  validate_args "${@:-}"
  if ! has_rust_scope; then
    log 'ℹ️  No Rust scope detected (no Cargo.toml); skipping coverage-rust.'
    return 0
  fi
  ensure_llvm_cov
  log '🔬 cargo llvm-cov clean...'
  cargo llvm-cov clean --workspace
  mkdir -p "${OUT_DIR}"
  log '🧪 cargo llvm-cov (workspace, all targets)...'
  run_coverage
  print_stats
  mkdir -p "${COVERAGE_DIR}"
  cp "${LCOV_PATH}" "${COVERAGE_DIR}/rust.lcov"
  log "📁 LCOV: ${LCOV_PATH} (bucketed at ${COVERAGE_DIR}/rust.lcov)"
  log "📁 HTML: ${OUT_DIR}/html/index.html"
}

function has_rust_scope() {
  [ -f 'Cargo.toml' ]
}

function ensure_llvm_cov() {
  if cargo llvm-cov --version >/dev/null 2>&1; then
    return 0
  fi
  log '❌ cargo-llvm-cov not installed (Rust scope detected).'
  log '   Install: cargo install cargo-llvm-cov && rustup component add llvm-tools-preview'
  exit 1
}

function run_coverage() {
  # Single test invocation, two report formats: --no-report runs the tests
  # and stores profraw; `report` re-emits without re-running.
  # NOTE: cargo-llvm-cov nests `html/` inside --output-dir, so pass OUT_DIR
  # (not OUT_DIR/html) to land at ${OUT_DIR}/html/index.html.
  #
  # Feature selection: honest coverage measurement requires including any
  # test gated behind a feature (and any integration test gated via
  # required-features on a binary). Default: `--all-features`. Override
  # via env `CARGO_COVERAGE_FEATURES="feat1 feat2"` when `--all-features`
  # is unsuitable (e.g., mutually-exclusive features like multiple BLAS
  # backends). Without an override, mutually-exclusive features will
  # surface as compilation failures — that's the signal to set the env.
  #
  # File exclusion: `cargo test` does not exercise `[[bin]]` main entry
  # points (`src/bin/*.rs`) — those run only via the built binary (and via
  # BATS/integration tests against the binary, which are not LCOV-
  # instrumented). Excluding bin/*.rs by default keeps the coverage rule
  # focused on library code where unit + integration tests do reach.
  # Override the exclusion via env `CARGO_COVERAGE_IGNORE_REGEX='...'`
  # (passed verbatim as --ignore-filename-regex).
  local feature_args=("--all-features")
  if [ -n "${CARGO_COVERAGE_FEATURES:-}" ]; then
    feature_args=("--features" "${CARGO_COVERAGE_FEATURES}")
  fi
  local ignore_regex="${CARGO_COVERAGE_IGNORE_REGEX:-src/bin/}"
  local ignore_args=("--ignore-filename-regex" "${ignore_regex}")
  cargo llvm-cov --workspace --all-targets "${feature_args[@]}" \
    "${ignore_args[@]}" --no-report
  cargo llvm-cov report --lcov --output-path "${LCOV_PATH}" \
    "${ignore_args[@]}"
  cargo llvm-cov report --html --output-dir "${OUT_DIR}" \
    "${ignore_args[@]}"
}

function print_stats() {
  local files lines covered pct
  files="$(grep -c '^SF:' "${LCOV_PATH}" || true)"
  lines="$(awk '/^LF:/{split($0,a,":"); tot+=a[2]} END{print tot+0}' "${LCOV_PATH}")"
  covered="$(awk '/^LH:/{split($0,a,":"); tot+=a[2]} END{print tot+0}' "${LCOV_PATH}")"
  if [ "${lines}" -gt 0 ]; then
    pct="$(awk -v c="${covered}" -v l="${lines}" 'BEGIN{printf "%.2f", 100*c/l}')"
  else
    pct='0.00'
  fi
  printf 'files:     %s\n' "${files}"
  printf 'lines:     %s\n' "${lines}"
  printf 'covered:   %s\n' "${covered}"
  printf 'total:     %s%%\n' "${pct}"
}

function log() {
  local msg
  msg="${1:-}"
  printf '%s\n' "${msg}" | tee -a '/tmp/keel_coverage_rust.log' >&5
}

function validate_args() {
  if [ "${#}" -gt 1 ] || [ -n "${1:-}" ]; then
    log '❌ Error: Unexpected argument'
    exit 1
  fi
}

main "${@:-}"
