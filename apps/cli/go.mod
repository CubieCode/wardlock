module wardlock.nz/cli

go 1.27.0

require wardlock.nz/apiclient v0.0.0-00010101000000-000000000000

// Until wardlock.nz serves go-import metadata (docs/DEPLOYMENT.md), resolve
// the client from this checkout. go.work covers workspace builds; this
// replace covers GOWORK=off builds and go mod tidy.
replace wardlock.nz/apiclient => ../../packages/apiclient-go
