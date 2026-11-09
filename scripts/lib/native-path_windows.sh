#!/usr/bin/env bash
# scripts/lib/native-path_windows.sh
# native_path on Windows; every other host uses native-path_posix.sh (governance/bash.md, Platform Variants).
# Source this file.

# native_path DIR: DIR as a PATH entry. PATH splits at ':', so a drive-letter path (D:/a/...) cannot be one;
# cygpath -u gives the bash runtime's own form (/d/a/... under MSYS2 and Git Bash, /cygdrive/d/a/... under
# Cygwin). cygpath ships with each of them; without it this fails, naming it.
function native_path() {
  if ! command -v cygpath > /dev/null 2>&1; then
    printf '❌ cygpath not found: it ships with Git for Windows, MSYS2 and Cygwin; native_path needs it\n' >&2
    return 1
  fi
  cygpath -u "${1}"
}
