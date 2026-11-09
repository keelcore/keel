# charts/keel/templates/_helpers.tpl
{{- define "keel.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "keel.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "keel.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" -}}
{{- end -}}

{{- define "keel.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- if .Values.serviceAccount.name -}}
{{- .Values.serviceAccount.name -}}
{{- else -}}
{{- include "keel.fullname" . -}}
{{- end -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
keel.image — the keel container image reference.
  repository: image.repository (default ghcr.io/keelcore/keel)
  tag:        image.tag, else "v" + .Chart.AppVersion (release tags are vX.Y.Z)
  flavor:     min / fips append "-min" / "-fips" to the tag; default and custom add nothing
  digest:     image.digest (sha256:...) pins the image and takes precedence over the tag
*/}}
{{- define "keel.image" -}}
{{- $img := .Values.image -}}
{{- if $img.digest -}}
{{- printf "%s@%s" $img.repository $img.digest -}}
{{- else -}}
{{- $tag := $img.tag | default (printf "v%s" (.Chart.AppVersion | trimPrefix "v")) -}}
{{- if has $img.flavor (list "min" "fips") -}}
{{- $tag = printf "%s-%s" $tag $img.flavor -}}
{{- else if not (has $img.flavor (list "default" "custom")) -}}
{{- fail (printf "image.flavor must be default, min, fips or custom; got %q" $img.flavor) -}}
{{- end -}}
{{- printf "%s:%s" $img.repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
keel.overlay — sets each key of .src in .dst, descending into a map both have; any other value replaces
.dst's, false, 0 and "" included (mergeOverwrite skips those). Changes .dst in place; renders nothing.
*/}}
{{- define "keel.overlay" -}}
{{- $dst := .dst -}}
{{- range $k, $v := .src -}}
{{- if and (kindIs "map" $v) (kindIs "map" (get $dst $k)) -}}
{{- include "keel.overlay" (dict "dst" (get $dst $k) "src" $v) -}}
{{- else -}}
{{- $_ := set $dst $k $v -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
keel.settings — keel.yaml below its keel: root, as YAML: values.keel without the chart's own keys (config,
secrets, mounts, extra*), then what the chart derives (sidecar.enabled from mode; file paths of the mounted
Secrets), then keel.config, each replacing what came before. Templates read listener ports from it too.
*/}}
{{- define "keel.settings" -}}
{{- $v := .Values.keel -}}
{{- $s := deepCopy (omit $v "config" "secrets" "mounts" "extraEnv" "extraArgs" "extraVolumeMounts" "extraVolumes") -}}
{{- $m := $v.mounts | default dict -}}
{{- $derived := dict "sidecar" (dict "enabled" (eq .Values.mode "sidecar")) -}}
{{- if $m.tlsSecret -}}
{{- include "keel.overlay" (dict "dst" $derived "src" (dict "tls" (dict "cert_file" "/etc/keel/tls/tls.crt" "key_file" "/etc/keel/tls/tls.key"))) -}}
{{- end -}}
{{- if $m.clientCASecret -}}
{{- include "keel.overlay" (dict "dst" $derived "src" (dict "tls" (dict "client_ca_file" "/etc/keel/client-ca/ca.crt"))) -}}
{{- end -}}
{{- if $m.upstreamTLSSecret -}}
{{- $files := dict "enabled" (eq .Values.mode "sidecar") "ca_file" "/etc/keel/secrets/upstream-tls/ca.crt" "client_cert_file" "/etc/keel/secrets/upstream-tls/tls.crt" "client_key_file" "/etc/keel/secrets/upstream-tls/tls.key" -}}
{{- include "keel.overlay" (dict "dst" $derived "src" (dict "sidecar" (dict "upstream_tls" $files))) -}}
{{- end -}}
{{- include "keel.overlay" (dict "dst" $s "src" $derived) -}}
{{- include "keel.overlay" (dict "dst" $s "src" (deepCopy ($v.config | default dict))) -}}
{{- toYaml $s -}}
{{- end -}}
