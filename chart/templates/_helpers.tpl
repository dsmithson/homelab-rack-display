{{- define "rack-display.labels" -}}
app.kubernetes.io/name: rack-display
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: homelab
{{- end }}
{{- define "rack-display.selector" -}}
app.kubernetes.io/name: rack-display
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
