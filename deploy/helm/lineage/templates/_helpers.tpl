{{- define "lineage.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "lineage.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "lineage.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "lineage.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "lineage.labels" -}}
helm.sh/chart: {{ include "lineage.chart" . }}
{{ include "lineage.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: lineage
{{- end -}}

{{- define "lineage.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lineage.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "lineage.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "lineage.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "lineage.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/*
Guard: SQLite is single-writer (§02.7), so more than one replica would corrupt/serialize
badly. Fail template rendering rather than ship a broken HA config.
*/}}
{{- define "lineage.validate" -}}
{{- if and (eq .Values.database.engine "sqlite") (gt (int .Values.replicaCount) 1) -}}
{{- fail "database.engine=sqlite requires replicaCount=1 (single-writer, §02.7). Use engine=postgres for HA." -}}
{{- end -}}
{{- if and .Values.autoscaling.enabled (eq .Values.database.engine "sqlite") -}}
{{- fail "autoscaling requires engine=postgres (SQLite is single-writer)." -}}
{{- end -}}
{{- if and (eq .Values.database.engine "postgres") (not .Values.database.postgres.dsnSecret.name) (not .Values.postgresql.enabled) -}}
{{- fail "database.engine=postgres needs database.postgres.dsnSecret.name (or postgresql.enabled subchart)." -}}
{{- end -}}
{{- end -}}

{{/*
Non-secret environment, shared by the app Deployment and the migrate Job. Secret-bearing
env (Postgres DSN, S3 keys) is added separately via secretKeyRef.
*/}}
{{- define "lineage.env" -}}
- name: LINEAGE_MODEL_API_ADDR
  value: ":{{ .Values.service.modelApiPort }}"
- name: LINEAGE_ADMIN_ADDR
  value: ":{{ .Values.service.adminUiPort }}"
- name: LINEAGE_METRICS_ADDR
  value: ":{{ .Values.service.opsPort }}"
- name: LINEAGE_DB_ENGINE
  value: {{ .Values.database.engine | quote }}
{{- if eq .Values.database.engine "sqlite" }}
- name: LINEAGE_DB_PATH
  value: {{ .Values.database.sqlite.path | quote }}
{{- else }}
- name: LINEAGE_DB_PATH
  valueFrom:
    secretKeyRef:
      name: {{ required "database.postgres.dsnSecret.name is required for postgres" .Values.database.postgres.dsnSecret.name }}
      key: {{ .Values.database.postgres.dsnSecret.key }}
{{- end }}
- name: LINEAGE_STORAGE_DRIVER
  value: {{ .Values.storage.driver | quote }}
{{- if eq .Values.storage.driver "fs" }}
- name: LINEAGE_STORAGE_ROOT
  value: {{ .Values.storage.fs.root | quote }}
{{- else }}
- name: LINEAGE_S3_BUCKET
  value: {{ .Values.storage.s3.bucket | quote }}
- name: LINEAGE_S3_REGION
  value: {{ .Values.storage.s3.region | quote }}
- name: LINEAGE_S3_ENDPOINT
  value: {{ .Values.storage.s3.endpoint | quote }}
- name: LINEAGE_S3_PATH_STYLE
  value: {{ .Values.storage.s3.pathStyle | quote }}
{{- if .Values.storage.s3.credentialsSecret.name }}
- name: LINEAGE_S3_ACCESS_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.storage.s3.credentialsSecret.name }}
      key: {{ .Values.storage.s3.credentialsSecret.accessKeyKey }}
- name: LINEAGE_S3_SECRET_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.storage.s3.credentialsSecret.name }}
      key: {{ .Values.storage.s3.credentialsSecret.secretKeyKey }}
{{- end }}
{{- end }}
- name: LINEAGE_CACHE_ENGINE
  value: {{ .Values.cache.engine | quote }}
{{- if eq .Values.cache.engine "redis" }}
- name: LINEAGE_REDIS_ADDR
  value: {{ required "cache.redis.addr is required for cache.engine=redis" .Values.cache.redis.addr | quote }}
- name: LINEAGE_REDIS_DB
  value: {{ .Values.cache.redis.db | quote }}
{{- if .Values.cache.redis.passwordSecret.name }}
- name: LINEAGE_REDIS_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ .Values.cache.redis.passwordSecret.name }}
      key: {{ .Values.cache.redis.passwordSecret.key }}
{{- end }}
{{- end }}
- name: LINEAGE_STORAGE_GC
  value: {{ .Values.storage.gc.mode | quote }}
- name: LINEAGE_GC_GRACE
  value: {{ .Values.storage.gc.grace | quote }}
- name: LINEAGE_GC_INTERVAL
  value: {{ .Values.storage.gc.interval | quote }}
- name: LINEAGE_GC_PREFIX
  value: {{ .Values.storage.gc.prefix | quote }}
- name: LINEAGE_ACTOR_HEADER
  value: {{ .Values.actorHeader | quote }}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/* Whether a data PVC is needed (sqlite, or fs storage). */}}
{{- define "lineage.needsPVC" -}}
{{- if or (eq .Values.database.engine "sqlite") (eq .Values.storage.driver "fs") -}}true{{- end -}}
{{- end -}}
