{{- define "lab-publisher.selectorLabels" -}}
app.kubernetes.io/name: lab-publisher
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "lab-publisher.labels" -}}
{{ include "lab-publisher.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end }}
