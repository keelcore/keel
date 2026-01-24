#!/usr/bin/env bats

# bash configuration per project discipline
set -o nounset
set -o errexit

setup() {
  PATH="./dist:$PATH"
}

@test "verify binary is shredded (under 3MB)" {
  size=$(stat -c%s "./dist/keel-min")
  [ "$size" -lt 3145728 ]
}

@test "verify fips build fails-closed without environment" {
  export FIPS_ENABLED=true
  run keel-fips
  [ "$status" -eq 1 ]
  [[ "$output" =~ "Failing closed" ]]
}
