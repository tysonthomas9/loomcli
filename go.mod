module github.com/tysonthomas9/loomcli

go 1.26

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/alicebob/miniredis/v2 v2.36.1
	github.com/creack/pty v1.1.24
	github.com/fsnotify/fsnotify v1.9.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/mattn/go-runewidth v0.0.19
	github.com/redis/go-redis/v9 v9.17.3
	github.com/spf13/cobra v1.10.2
	github.com/tysonthomas9/fleet-db v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otel v1.41.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.41.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.41.0
	go.opentelemetry.io/otel/metric v1.41.0
	go.opentelemetry.io/otel/sdk v1.41.0
	go.opentelemetry.io/otel/sdk/metric v1.41.0
	go.opentelemetry.io/otel/trace v1.41.0
	golang.org/x/net v0.50.0
	golang.org/x/sys v0.41.0
	golang.org/x/time v0.14.0
	gopkg.in/yaml.v3 v3.0.1
	nhooyr.io/websocket v1.8.17
)

require (
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/tysonthomas9/fleet-db v0.0.0-00010101000000-000000000000 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.41.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/grpc v1.79.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/tysonthomas9/fleet-db => ../../../fleet-db
