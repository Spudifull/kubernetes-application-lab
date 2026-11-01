{{- define "lab-worker.selectorLabels" -}}
app.kubernetes.io/name: lab-worker
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "lab-worker.labels" -}}
{{ include "lab-worker.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end }}
