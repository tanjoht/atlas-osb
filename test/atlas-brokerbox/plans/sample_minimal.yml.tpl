name: minimal-plan
description: This is a minimal plan for a cluster
free: true
apiKey: {{ json (index .Credentials.Orgs "5ea0477597999053a5f9cbec") }}
project:
  name: {{ .InstanceID}}
  desc: "{{ .InstanceID}} description"
cluster:
  name: {{ .InstanceID }} 
  providerSettings:
    regionName: "US_EAST_1"
    providerName: "AWS"
    instanceSizeName: M20
