package main

import "strings"

var (
	templates = map[string][]byte{
		"plain-text": []byte(plainTextTemplate),
		"json":       []byte(jsonTemplate),
	}
)

type format interface{}
type knownFormat string

func (kf knownFormat) template() []byte {
	return templates[string(kf)]
}

type filePath string
type userDefinedTemplate filePath

func formatFromString(formatSpec string) format {
	const prefix = "path:"
	if strings.HasPrefix(formatSpec, prefix) {
		return userDefinedTemplate(formatSpec[len(prefix):])
	}
	switch formatSpec {
	case "pt", "plain-text":
		return knownFormat("plain-text")
	case "j", "json":
		return knownFormat("json")
	}
	// nil represents unknown format
	return nil
}

const (
	plainTextTemplate = `
{{- printf "%10v %10v %10v %10v" "Statistics" "Avg" "Stdev" "Max" }}
{{ with .Result.RequestsStats (FloatsToArray 0.5 0.75 0.9 0.95 0.99) }}
	{{- printf "  %-10v %10.2f %10.2f %10.2f" "Reqs/sec" .Mean .Stddev .Max -}}
{{ else }}
	{{- print "  There wasn't enough data to compute statistics for requests." }}
{{ end }}
{{ with .Result.LatenciesStats (FloatsToArray 0.5 0.75 0.9 0.95 0.99) }}
	{{- printf "  %-10v %10v %10v %10v" "Latency" (FormatTimeUs .Mean) (FormatTimeUs .Stddev) (FormatTimeUs .Max) }}
	{{- if WithLatencies }}
  		{{- "\n  Latency Distribution (Total)" }}
		{{- range $pc, $lat := .Percentiles }}
			{{- printf "\n     %2.0f%% %10s" (Multiply $pc 100) (FormatTimeUsUint64 $lat) -}}
		{{ end -}}
		{{- if .Percentiles2xx }}
			{{- "\n  Latency Distribution (2xx)" }}
			{{- range $pc, $lat := .Percentiles2xx }}
				{{- printf "\n     %2.0f%% %10s" (Multiply $pc 100) (FormatTimeUsUint64 $lat) -}}
			{{ end -}}
		{{ end -}}
	{{ end }}
{{ else }}
	{{- print "  There wasn't enough data to compute statistics for latencies." }}
{{ end -}}
{{ with .Result -}}
{{ "  HTTP codes:" }}
{{ printf "    1xx - %v, 2xx - %v, 3xx - %v, 4xx - %v, 5xx - %v" .Req1XX .Req2XX .Req3XX .Req4XX .Req5XX }}
	{{- printf "\n    others - %v" .Others }}
	{{- with .Errors }}
		{{- "\n  Errors:"}}
		{{- range . }}
			{{- printf "\n    %10v - %v" .Error .Count }}
		{{- end -}}
	{{ end -}}
{{ end }}
{{ printf "  %-10v %10v/s\n" "Throughput:" (FormatBinary .Result.Throughput)}}`
	jsonTemplate = `{"spec":{
{{- with .Spec -}}
"numberOfConnections":{{ .NumberOfConnections }}

{{- if .IsTimedTest -}}
,"testType":"timed","testDurationSeconds":{{ .TestDuration.Seconds }}
{{- else -}}
,"testType":"number-of-requests","numberOfRequests":{{ .NumberOfRequests }}
{{- end -}}

,"method":"{{ .Method }}","url":{{ .URL | printf "%q" }}

{{- with .Headers -}}
,"headers":[
{{- range $index, $header :=  . -}}
{{- if ne $index 0 -}},{{- end -}}
{"key":{{ .Key | printf "%q" }},"value":{{ .Value | printf "%q" }}}
{{- end -}}
]
{{- end -}}

{{- if .BodyFilePath -}}
,"bodyFilePath":{{ .BodyFilePath | printf "%q" }}
{{- else -}}
,"body":{{ .Body | printf "%q" }}
{{- end -}}

{{- if .CertPath -}}
,"certPath":{{ .CertPath | printf "%q" }}
{{- end -}}
{{- if .KeyPath -}}
,"keyPath":{{ .KeyPath | printf "%q" }}
{{- end -}}

,"stream":{{ .Stream }},"timeoutSeconds":{{ .Timeout.Seconds }}

{{- if .IsFastHTTP -}}
,"client":"fasthttp"
{{- end -}}
{{- if .IsNetHTTPV1 -}}
,"client":"net/http.v1"
{{- end -}}
{{- if .IsNetHTTPV2 -}}
,"client":"net/http.v2"
{{- end -}}

{{- with .Rate -}}
,"rate":{{ . }}
{{- end -}}
{{- end -}}
},

{{- with .Result -}}
"result":{"bytesRead":{{ .BytesRead -}}
,"bytesWritten":{{ .BytesWritten -}}
,"timeTakenSeconds":{{ .TimeTaken.Seconds -}}

,"req1xx":{{ .Req1XX -}}
,"req2xx":{{ .Req2XX -}}
,"req3xx":{{ .Req3XX -}}
,"req4xx":{{ .Req4XX -}}
,"req5xx":{{ .Req5XX -}}
,"others":{{ .Others -}}

{{- with .Errors -}}
,"errors":[
{{- range $index, $error :=  . -}}
{{- if ne $index 0 -}},{{- end -}}
{"description":{{ .Error | printf "%q" }},"count":{{ .Count }}}
{{- end -}}
]
{{- end -}}

{{- with .LatenciesStats (FloatsToArray 0.5 0.75 0.9 0.95 0.99) -}}
,"latency":{"mean":{{ .Mean -}}
,"stddev":{{ .Stddev -}}
,"max":{{ .Max -}}

{{- if WithLatencies -}}
,"percentiles":{
{{- range $pc, $lat := .Percentiles }}
{{- if ne $pc 0.5 -}},{{- end -}}
{{- printf "\"%2.0f\":%d" (Multiply $pc 100) $lat -}}
{{- end -}}
},
"percentiles2xx":{
{{- range $pc, $lat := .Percentiles2xx }}
{{- if ne $pc 0.5 -}},{{- end -}}
{{- printf "\"%2.0f\":%d" (Multiply $pc 100) $lat -}}
{{- end -}}
}
{{- end -}}

}
{{- end -}}

{{- with .RequestsStats (FloatsToArray 0.5 0.75 0.9 0.95 0.99) -}}
,"rps":{"mean":{{ .Mean -}}
,"stddev":{{ .Stddev -}}
,"max":{{ .Max -}}
,"percentiles":{
{{- range $pc, $rps := .Percentiles }}
{{- if ne $pc 0.5 -}},{{- end -}}
{{- printf "\"%2.0f\":%f" (Multiply $pc 100) $rps -}}
{{- end -}}
}}
{{- end -}}
}}
{{- end -}}`
)
