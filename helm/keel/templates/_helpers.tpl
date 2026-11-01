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
