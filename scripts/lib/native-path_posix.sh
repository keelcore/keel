#!/usr/bin/env bash
# scripts/lib/native-path_posix.sh
# native_path on every host but Windows; the Windows variant is native-path_windows.sh (governance/bash.md,
# Platform Variants). Source this file.

# native_path DIR: DIR as a PATH entry. A POSIX path is one already.
function native_path() {
  printf '%s\n' "${1}"
}
