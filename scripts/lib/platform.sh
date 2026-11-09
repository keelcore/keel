#!/usr/bin/env bash
# scripts/lib/platform.sh
# The host platform, for choosing between a library's platform variants (governance/bash.md, Platform
# Variants): scripts/lib/<name>_windows.sh on Windows, scripts/lib/<name>_posix.sh everywhere else. Source
# this file.

# host_platform: windows under the Windows bash runtimes (MSYS2, Git Bash, Cygwin), posix otherwise.
function host_platform() {
  case "$(uname -s)" in
    MINGW* | MSYS* | CYGWIN*) printf 'windows' ;;
    *) printf 'posix' ;;
  esac
}
