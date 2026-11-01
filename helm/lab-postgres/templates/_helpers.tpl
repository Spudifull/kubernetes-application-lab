{{- define "lab-postgres.labels" -}}
app.kubernetes.io/name: lab-postgres
app.kubernetes.io/instance: {{ .Release.Name | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kubernetes-application-lab
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
{{- end -}}
{{- define "lab-postgres.selectorLabels" -}}
app.kubernetes.io/name: lab-postgres
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}
