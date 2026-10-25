module wardlock.nz/cli

go 1.27.0

require (
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/term v0.47.0
	golang.org/x/text v0.43.0
	wardlock.nz/apiclient v0.0.0-00010101000000-000000000000
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/oapi-codegen/runtime v1.7.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

// Until wardlock.nz serves go-import metadata (docs/DEPLOYMENT.md), resolve
// the client from this checkout. go.work covers workspace builds; this
// replace covers GOWORK=off builds and go mod tidy.
replace wardlock.nz/apiclient => ../../packages/apiclient-go
