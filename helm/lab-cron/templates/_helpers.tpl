{{- define "lab-cron.labels" -}}
app.kubernetes.io/name: lab-cron
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
