#!/usr/bin/env bash
# scripts/lib/env-imports.sh
# governance/bash.md R37: every environment variable a bash file reads is imported once, at file scope before
# `function main`, as `declare -rx NAME="${NAME:-<default>}"`, and listed in the file's header as
# `NAME (default <default>)`, or `NAME (default empty)`. Files are sourced with `source`, never `.`. Read by
# lint-bash.sh. Source this file.
#
# A read is an uppercase ${NAME...} or $NAME outside comments, single-quoted text and quoted heredocs. NAME is
# an environment variable when the file does not assign it, no file it sources (transitively) assigns or
# imports it, and it is not one of ENV_IMPORTS_EXEMPT, which the shell or the OS sets for every process.

declare -r ENV_IMPORTS_EXEMPT='PATH HOME PWD OLDPWD TMPDIR USER LOGNAME SHELL TERM LANG LC_ALL IFS BASH BASHPID
BASH_ARGC BASH_ARGV BASH_LINENO BASH_REMATCH BASH_SOURCE BASH_SUBSHELL BASH_VERSINFO BASH_VERSION FUNCNAME
LINENO PIPESTATUS RANDOM SECONDS UID EUID PPID HOSTNAME HOSTTYPE OSTYPE MACHTYPE SHLVL OPTARG OPTIND REPLY
COLUMNS LINES'

# check_env_imports FILE...: prints one `FILE:LINE: NAME: problem` line per non-conforming site; nothing when
# every file conforms. BATS_* names, which the bats runtime sets, are exempt too.
function check_env_imports() {
  [ "${#}" -gt 0 ] || return 0
  local records edges
  records="$(env_import_records "$@")"
  edges="$(env_import_edges "${records}")"
  printf '%s\n%s\n' "${records}" "${edges}" | env_import_findings
}

# env_import_records FILE...: per file, tab-separated records:
#   REF FILE LINE NAME DEFAULT    a read (DEFAULT: the text after :- when the read has one, else empty)
#   SET FILE LINE NAME            a name the file assigns
#   IMP FILE LINE NAME DEFAULT SCOPE   a conforming import; SCOPE: file, function or after-main
#   REN FILE LINE NAME            an import of NAME under another name
#   BAD FILE LINE NAME DEFAULT    an import of NAME under its own name, not as declare -rx
#   SRC FILE LINE PATH            a `source PATH` line
#   DOT FILE LINE                 a `. PATH` line
#   HDR FILE TEXT                 the header comment block, on one line
function env_import_records() {
  awk '
    function flush_header() { if (!hdr_done) { printf "HDR\t%s\t%s\n", FILENAME, hdr; hdr_done = 1 } }
    # visible: the line without comments, single-quoted text and escaped dollars; quote state carries over.
    # q: "" (unquoted), "s" (single), "a" ($'...'), "d" (double). A $( inside double quotes opens an unquoted
    # level (lvl), whose closing ) returns to the double quotes; dep[lvl] counts its open parentheses.
    function visible(s,    out, i, c, n, prev) {
      out = ""; n = length(s); prev = " "
      for (i = 1; i <= n; i++) {
        c = substr(s, i, 1)
        if (q == "s") { if (c == "\047") q = ""; prev = c; continue }
        if (q == "a") { if (c == "\\") { i++; continue } if (c == "\047") q = ""; prev = c; continue }
        if (c == "\\") { i++; prev = "x"; continue }
        if (q == "d") {
          if (c == "$" && substr(s, i + 1, 1) == "(") { lvl++; dep[lvl] = 1; q = ""; out = out "$("; i++; prev = "("; continue }
          if (c == "\"") q = ""
          out = out c; prev = c; continue
        }
        if (c == "#" && prev ~ /[ \t;|&(]/) break
        if (c == "$" && substr(s, i + 1, 1) == "\047") { q = "a"; i++; prev = c; continue }
        if (c == "\047") { q = "s"; prev = c; continue }
        if (c == "\"") q = "d"
        if (lvl > 0 && c == "(") dep[lvl]++
        if (lvl > 0 && c == ")" && --dep[lvl] == 0) { lvl--; q = "d" }
        out = out c; prev = c
      }
      return out
    }
    # refs: each uppercase ${NAME...} or $NAME; a name with a lowercase letter is a local, not a read.
    function refs(v,    rest, name, def, close_at) {
      rest = v
      while (match(rest, /\$\{[#!]?[A-Za-z_][A-Za-z0-9_]*|\$[A-Za-z_][A-Za-z0-9_]*/)) {
        name = substr(rest, RSTART, RLENGTH); sub(/^\$\{?[#!]?/, "", name)
        rest = substr(rest, RSTART + RLENGTH); def = ""
        if (name !~ /^[A-Z_][A-Z0-9_]*$/ || name == "_") continue
        if (substr(rest, 1, 2) == ":-") { close_at = index(rest, "}"); if (close_at) def = substr(rest, 3, close_at - 3) }
        printf "REF\t%s\t%d\t%s\t%s\n", FILENAME, FNR, name, def
      }
    }
    function sets(v,    w, n, i, k, rest) {
      rest = v
      while (match(rest, /(^|[ \t;&|({])[A-Z_][A-Z0-9_]*(\[[^]]*\])?\+?=/)) {
        w = substr(rest, RSTART, RLENGTH); sub(/^[ \t;&|({]/, "", w); sub(/(\[.*)?\+?=$/, "", w)
        printf "SET\t%s\t%d\t%s\n", FILENAME, FNR, w; rest = substr(rest, RSTART + RLENGTH)
      }
      n = split(v, k, /[ \t;]+/)
      for (i = 1; i <= n; i++) {
        if (k[i] ~ /^(declare|local|readonly|export|typeset)$/) {
          for (i++; i <= n && k[i] ~ /^-/; i++) ;
          for (; i <= n && k[i] ~ /^[A-Za-z_][A-Za-z0-9_]*(=|\[|$)/; i++) {
            w = k[i]; sub(/[=[].*$/, "", w); if (w ~ /^[A-Z_][A-Z0-9_]*$/) printf "SET\t%s\t%d\t%s\n", FILENAME, FNR, w
            if (k[i] ~ /=/) break
          }
        } else if (k[i] == "for" && k[i + 1] ~ /^[A-Z_][A-Z0-9_]*$/) {
          printf "SET\t%s\t%d\t%s\n", FILENAME, FNR, k[i + 1]
        } else if (k[i] == "read" || (k[i] == "printf" && k[i + 1] == "-v")) {
          for (i++; i <= n && k[i] ~ /^[-A-Za-z_0-9]+$/; i++) if (k[i] ~ /^[A-Z_][A-Z0-9_]*$/) printf "SET\t%s\t%d\t%s\n", FILENAME, FNR, k[i]
        }
      }
    }
    FNR == 1 { q = ""; lvl = 0; doc = ""; docq = 0; infunc = 0; main_at = 0; hdr = ""; hdr_done = 0; next }
    !hdr_done && /^#/ { t = $0; sub(/^#[ \t]?/, "", t); hdr = hdr " " t; next }
    { flush_header() }
    doc != "" {
      if ($0 == doc || ($0 ~ /^\t+/ && substr($0, match($0, /[^\t]/)) == doc)) { doc = ""; next }
      if (!docq) refs($0)
      next
    }
    {
      line = $0; trimmed = line; sub(/^[ \t]+/, "", trimmed); sub(/[ \t]+$/, "", trimmed)
      if (line ~ /^(function[ \t]+)?[A-Za-z_][A-Za-z0-9_]*\(\)/) { infunc = 1; if (line ~ /^function[ \t]+main\(\)/ && !main_at) main_at = FNR }
      starts_in_quote = (q != "")
      v = visible(line)
      if (!starts_in_quote && match(trimmed, /^declare -rx [A-Z_][A-Z0-9_]*="\$\{[A-Z_][A-Z0-9_]*:-.*\}"$/)) {
        lhs = trimmed; sub(/^declare -rx /, "", lhs); sub(/=.*$/, "", lhs)
        rhs = trimmed; sub(/^[^{]*\{/, "", rhs); sub(/:-.*$/, "", rhs)
        def = trimmed; sub(/^[^{]*\{[A-Z_][A-Z0-9_]*:-/, "", def); sub(/\}"$/, "", def)
        if (lhs == rhs) {
          scope = infunc ? "function" : (main_at ? "after-main" : "file")
          printf "IMP\t%s\t%d\t%s\t%s\t%s\n", FILENAME, FNR, lhs, def, scope
          if (line ~ /^\}/) infunc = 0
          next
        }
        printf "REN\t%s\t%d\t%s\n", FILENAME, FNR, rhs
      } else if (!starts_in_quote && match(trimmed, /^((readonly|declare|local|export)( -[a-zA-Z]+)* )?[A-Za-z_][A-Za-z0-9_]*="\$\{[A-Z_][A-Z0-9_]*:?[-=]/)) {
        lhs = trimmed; sub(/^((readonly|declare|local|export)( -[a-zA-Z]+)* )?/, "", lhs); sub(/=.*$/, "", lhs)
        rhs = trimmed; sub(/^[^{]*\{/, "", rhs); sub(/:?[-=].*$/, "", rhs)
        if (lhs != rhs) printf "REN\t%s\t%d\t%s\n", FILENAME, FNR, rhs
        else { def = trimmed; sub(/^[^{]*\{[A-Z_][A-Z0-9_]*:?[-=]/, "", def); sub(/\}"$/, "", def)
          printf "BAD\t%s\t%d\t%s\t%s\n", FILENAME, FNR, rhs, def }
      }
      if (!starts_in_quote && match(trimmed, /^source[ \t]/)) printf "SRC\t%s\t%d\t%s\n", FILENAME, FNR, substr(trimmed, 8)
      if (!starts_in_quote && trimmed ~ /^\.[ \t]+[^ \t]/) printf "DOT\t%s\t%d\n", FILENAME, FNR
      refs(v); sets(v)
      if (q == "" && match(line, /<<-?[ \t]*["\047]?[A-Za-z_][A-Za-z0-9_]*["\047]?/) && substr(line, RSTART + 2, 1) != "<") {
        doc = substr(line, RSTART, RLENGTH); docq = (doc ~ /["\047]/); sub(/^<<-?[ \t]*["\047]?/, "", doc); sub(/["\047]$/, "", doc)
      }
      if (line ~ /^\}/ || (infunc && line ~ /^(function[ \t]+)?[A-Za-z_][A-Za-z0-9_]*\(\).*\}[ \t]*$/)) infunc = 0
    }
  ' "$@"
}

# env_import_edges RECORDS: `EDGE FILE LIB` for each `source` line whose path resolves to a file: a path naming
# scripts/lib/NAME.sh is that file; a path relative to the file's own directory is resolved from it.
function env_import_edges() {
  local kind file path target
  while IFS=$'\t' read -r kind file _ path; do
    [ "${kind}" = 'SRC' ] || continue
    target="$(env_source_target "${file}" "${path}")"
    [ -n "${target}" ] && [ -f "${target}" ] && printf 'EDGE\t%s\t%s\n' "${file}" "${target}"
  done <<< "${1}"
}

# env_source_target FILE PATH: the file `source PATH` in FILE names, relative to the repository root, whether or
# not it exists; nothing when PATH is not one of the two forms env_import_edges resolves.
function env_source_target() {
  local -r file="${1}" path="${2}"
  local rel dir
  if [[ "${path}" =~ scripts/lib/([A-Za-z0-9_.-]+\.sh) ]]; then
    printf 'scripts/lib/%s' "${BASH_REMATCH[1]}"
  elif [[ "${path}" =~ ^\"\$\((dirname|cd).*\)\"?(/[A-Za-z0-9_./-]+\.sh)\"$ ]]; then
    rel="${BASH_REMATCH[2]}"
    dir="$(cd "$(dirname "${file}")$(dirname "${rel}")" 2> /dev/null && pwd -P)" || return 0
    printf '%s/%s' "${dir#"$(pwd -P)/"}" "$(basename "${rel}")"
  fi
}

# env_import_chain SCRIPT: the names SCRIPT imports (R37), with every file it sources, its project hook (the same
# path with .local.sh, when there is one) and every scripts/.../*.sh it runs by path, transitively; one per line.
# A file in the chain that is missing or unreadable fails, naming it on stderr. Run from the repository root.
function env_import_chain() {
  local -a queue=("${1}")
  local seen=' ' names='' f i=0 line dep
  while [ "${i}" -lt "${#queue[@]}" ]; do
    f="${queue[${i}]}"
    i=$((i + 1))
    case "${seen}" in *" ${f} "*) continue ;; esac
    seen+="${f} "
    if [ ! -r "${f}" ]; then
      printf '❌ %s: not readable; the environment it imports cannot be passed\n' "${f}" >&2
      return 1
    fi
    names+="$(sed -n 's/^declare -rx \([A-Z_][A-Z0-9_]*\)="\${\1:-.*}"$/\1/p' "${f}")"$'\n'
    [[ "${f}" == *.local.sh ]] || [ ! -f "${f%.sh}.local.sh" ] || queue+=("${f%.sh}.local.sh")
    while IFS= read -r line; do
      dep="$(env_source_target "${f}" "${line#source }")"
      [ -z "${dep}" ] || queue+=("${dep}")
    done < <(sed -n 's/^[[:space:]]*\(source .*\)$/\1/p' "${f}")
    while IFS= read -r dep; do
      [ ! -f "${dep}" ] || queue+=("${dep}")
    done < <(grep -v '^[[:space:]]*#' "${f}" | grep -oE 'scripts/[A-Za-z0-9_./-]+\.sh' || true)
  done
  printf '%s' "${names}" | sed '/^$/d' | sort -u
}

# env_import_findings: reads the records and edges on stdin; prints the findings, sorted.
function env_import_findings() {
  awk -F '\t' -v exempt="${ENV_IMPORTS_EXEMPT}" '
    BEGIN { n = split(exempt, e, /[ \n\t]+/); for (i = 1; i <= n; i++) EX[e[i]] = 1 }
    function write(name, def) { return sprintf("write at file scope: declare -rx %s=\"${%s:-%s}\"", name, name, def == "" ? "<default>" : def) }
    $1 == "REF" { R[++nr] = $2 SUBSEP $3 SUBSEP $4; RD[nr] = $5; FILES[$2] = 1 }
    $1 == "SET" { SET[$2, $4] = 1; SETLN[$2, $4] = SETLN[$2, $4] ? SETLN[$2, $4] : $3; FILES[$2] = 1 }
    $1 == "IMP" { I[++ni] = $2 SUBSEP $3 SUBSEP $4 SUBSEP $5 SUBSEP $6; FILES[$2] = 1 }
    $1 == "REN" { REN[$2, $3] = $4 }
    $1 == "BAD" { printf "%s:%d: %s: imported without declare -rx; %s\n", $2, $3, $4, write($4, $5) }
    $1 == "DOT" { printf "%s:%d: .: source files with `source`, not `.`\n", $2, $3 }
    $1 == "HDR" { HDR[$2] = $3 }
    $1 == "EDGE" { EDGE[++ne] = $2 SUBSEP $3 }
    END {
      # PROV[f, NAME]: assigned or imported by a file f sources, transitively; BY[f, NAME]: that file.
      # REACH[f, g]: f sources g, directly or through others. A worklist per f, never a loop over an array it
      # adds to (undefined in awk).
      for (k = 1; k <= ne; k++) { split(EDGE[k], p, SUBSEP); ADJ[p[1]] = ADJ[p[1]] " " p[2] }
      for (f in ADJ) {
        nw = split(ADJ[f], W, " ")
        for (j = 1; j <= nw; j++) {
          if ((f, W[j]) in REACH) continue
          REACH[f, W[j]] = 1
          m = split(ADJ[W[j]], nx, " ")
          for (i = 1; i <= m; i++) W[++nw] = nx[i]
        }
      }
      # OWNS[g]: the names g assigns or imports, space-separated.
      for (k in SET) { split(k, p, SUBSEP); OWNS[p[1]] = OWNS[p[1]] " " p[2] }
      for (k = 1; k <= ni; k++) { split(I[k], p, SUBSEP); OWNS[p[1]] = OWNS[p[1]] " " p[3] }
      for (a in REACH) { split(a, p, SUBSEP)
        m = split(OWNS[p[2]], nx, " ")
        for (i = 1; i <= m; i++) { PROV[p[1], nx[i]] = 1; BY[p[1], nx[i]] = p[2] } }
      # An import is read-only: assigning the name afterwards, in the file or in one that sources it, fails at
      # run time, and so does sourcing the importing file twice in one process.
      for (k = 1; k <= ni; k++) { split(I[k], p, SUBSEP); IMPOF[p[1], p[3]] = p[2]; IMPFILE[p[1]] = 1 }
      for (s in SET) { split(s, p, SUBSEP); f = p[1]; name = p[2]
        if ((f, name) in IMPOF) printf "%s:%d: %s: assigned, but imported read-only on line %d\n", f, SETLN[f, name], name, IMPOF[f, name]
        for (a in REACH) { split(a, r, SUBSEP)
          if (r[1] == f && (r[2], name) in IMPOF) printf "%s:%d: %s: assigned, but %s imports it read-only\n", f, SETLN[f, name], name, r[2] } }
      for (k = 1; k <= ne; k++) { split(EDGE[k], p, SUBSEP); EP[p[1], p[2]] = 1; TOP[p[1]] = 1 }
      for (f in TOP) for (lib in IMPFILE) {
        c = ((f, lib) in EP) ? 1 : 0
        for (a in REACH) { split(a, r, SUBSEP); if (r[1] == f && (r[2], lib) in EP) c++ }
        if (c > 1) printf "%s:1: %s: sourced more than once in this chain; its imports are read-only\n", f, lib
      }
      for (k = 1; k <= ni; k++) {
        split(I[k], p, SUBSEP); f = p[1]; ln = p[2]; name = p[3]; def = p[4]; scope = p[5]
        if (scope == "function") printf "%s:%d: %s: imported inside a function; %s\n", f, ln, name, write(name, def)
        else if (scope == "after-main") printf "%s:%d: %s: imported after function main; %s\n", f, ln, name, write(name, def)
        else FILEIMP[f, name] = 1
        if (SEEN[f, name]++) printf "%s:%d: %s: imported twice in this file\n", f, ln, name
        if ((f, name) in PROV) printf "%s:%d: %s: already imported in this sourcing chain, by %s\n", f, ln, name, BY[f, name]
        if (scope == "file") {
          # The header lists NAME (default D), where ), ; or , ends D.
          h = " " HDR[f] " "; stem = name " (default " (def == "" ? "empty" : def); want = stem ")"
          at = index(h, stem); after = at ? substr(h, at + length(stem), 1) : ""
          if (index(h, " " name " ") == 0 && index(h, " " name ",") == 0 && index(h, " " name " (") == 0 && index(h, " " name ":") == 0)
            printf "%s:%d: %s: the header does not list it; add %s\n", f, ln, name, want
          else if (after !~ /^[);,]$/) printf "%s:%d: %s: the header does not give its default; write %s\n", f, ln, name, want
        }
      }
      for (k = 1; k <= nr; k++) {
        split(R[k], p, SUBSEP); f = p[1]; ln = p[2]; name = p[3]
        if (name in EX || name ~ /^BATS_/) continue
        if ((f, name) in SET || (f, name) in PROV || (f, name) in FILEIMP) continue
        if ((f, ln) in REN && REN[f, ln] == name) { if (!DONE[f, ln, name]++) printf "%s:%d: %s: imported under another name; %s\n", f, ln, name, write(name, RD[k]) }
        else if (!DONE[f, ln, name]++) printf "%s:%d: %s: read without an import; %s\n", f, ln, name, write(name, RD[k])
      }
    }
  ' | sort -t: -k1,1 -k2,2n -k3 | uniq
}
