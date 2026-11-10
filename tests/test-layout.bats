#!/usr/bin/env bats
# tests/test-layout.bats
# The unit-test layout check (scripts/lint/lint-test-layout.local.sh, #40): all unit tests for {foo}.go are in
# {foo}_test.go beside it. Each test builds a small git tree, as the check reads tracked files, and runs the check
# in it.

setup_file() {
  REPO_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." && pwd)"
  export REPO_ROOT
}

setup() {
  TREE="${BATS_TEST_TMPDIR}/tree"
  mkdir -p "${TREE}"
  git -C "${TREE}" init -q
}

# put PATH CONTENT: a tracked file in the tree.
put() {
  mkdir -p "$(dirname "${TREE}/${1}")"
  printf '%s\n' "${2}" > "${TREE}/${1}"
  git -C "${TREE}" add "${1}"
}

check() {
  run bash -c "cd '${TREE}' && bash '${REPO_ROOT}/scripts/lint/lint-test-layout.local.sh'"
  echo "${output}"
}

@test "test layout: passes when every source has its test file and every test file its source" {
  put a.go 'package a'
  put a_test.go 'package a'
  put pkg/b/b.go 'package b'
  put pkg/b/b_test.go 'package b'
  check
  [ "${status}" -eq 0 ]
}

@test "test layout: fails, naming it, on a test file with no source" {
  put a.go 'package a'
  put a_test.go 'package a'
  put stray_test.go 'package a'
  check
  [ "${status}" -ne 0 ]
  [[ "${output}" == *stray_test.go* ]]
}

@test "test layout: fails, naming it, on a source with no test file" {
  put a.go 'package a'
  put b.go 'package a'
  put a_test.go 'package a'
  check
  [ "${status}" -ne 0 ]
  [[ "${output}" == *b.go* ]]
}

@test "test layout: main.go and doc.go need no test file" {
  put cmd/x/main.go 'package main'
  put doc.go 'package a'
  put pkg/b/doc.go 'package b'
  check
  [ "${status}" -eq 0 ]
}

@test "test layout: a tag-named test file built under its tag pairs with its source" {
  put a.go 'package a'
  put a_test.go 'package a'
  put a_notwindows_test.go $'//go:build !windows\n\npackage a'
  put a_not_no_h3_test.go $'//go:build !no_h3\n\npackage a'
  put a_no_h3_test.go $'//go:build no_h3\n\npackage a'
  check
  [ "${status}" -eq 0 ]
}

@test "test layout: a tag-named test file with no build constraint has no source" {
  put a.go 'package a'
  put a_test.go 'package a'
  put a_extra_test.go 'package a'
  check
  [ "${status}" -ne 0 ]
  [[ "${output}" == *a_extra_test.go* ]]
}

@test "test layout: paths listed in config/test-layout.local.txt are not checked" {
  put config/test-layout.local.txt $'# integration suites\n^tests/compose/'
  put tests/compose/stack.go 'package compose'
  put tests/compose/stray_test.go 'package compose'
  check
  [ "${status}" -eq 0 ]
}

@test "test layout: this repository passes, and a stray test file in it fails" {
  (cd "${REPO_ROOT}" && git ls-files --cached --others --exclude-standard | while IFS= read -r f; do
    [ -f "${f}" ] && printf '%s\n' "${f}"
  done | tar -cf - -T -) | tar -x -C "${TREE}"
  git -C "${TREE}" add -A
  check
  [ "${status}" -eq 0 ]
  put pkg/core/stray_test.go 'package core'
  check
  [ "${status}" -ne 0 ]
  [[ "${output}" == *pkg/core/stray_test.go* ]]
}
