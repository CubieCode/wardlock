module wardlock.nz/server

go 1.27.0

require (
	github.com/caddyserver/certmagic v0.25.6
	github.com/coreos/go-oidc/v3 v3.21.0
	github.com/go-jose/go-jose/v4 v4.1.4
	github.com/go-webauthn/webauthn v0.18.2
	github.com/google/uuid v1.6.0
	github.com/letsencrypt/pebble/v2 v2.10.1
	github.com/libdns/cloudflare v0.2.2
	github.com/libdns/desec v1.1.1
	github.com/libdns/libdns v1.1.1
	github.com/miekg/dns v1.1.73
	github.com/minio/minio-go/v7 v7.4.1
	github.com/oapi-codegen/runtime v1.7.0
	github.com/oschwald/maxminddb-golang/v2 v2.8.0
	go.uber.org/zap v1.28.0
	golang.org/x/crypto v0.57.0
	golang.org/x/net v0.60.0
	golang.org/x/oauth2 v0.37.0
	golang.org/x/text v0.42.0
	modernc.org/sqlite v1.60.1
	wardlock.nz/apiclient v0.0.0-00010101000000-000000000000
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/caddyserver/zerossl v0.1.6 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.4 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.3.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/letsencrypt/challtestsrv v1.4.2 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mholt/acmez/v3 v3.1.7 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/zeebo/blake3 v0.2.4 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap/exp v0.3.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.48.0 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

// Until wardlock.nz serves go-import metadata (docs/DEPLOYMENT.md), resolve
// the MIT client from this checkout. go.work covers workspace builds; this
// replace covers GOWORK=off builds and go mod tidy.
replace wardlock.nz/apiclient => ../../packages/apiclient-go
