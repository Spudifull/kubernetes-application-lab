{{- define "dependency.selector" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/instance: lab-dependency-metrics
{{- end }}
{{- define "dependency.labels" -}}
{{ include "dependency.selector" . }}
app.kubernetes.io/managed-by: Helm
{{- end }}
