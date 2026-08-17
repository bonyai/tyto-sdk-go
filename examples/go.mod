// The examples are their own module so that `go get github.com/bonyai/tyto-go`
// never pulls them in, and so a reader can copy one out and run it unchanged.
module github.com/bonyai/tyto-go/examples

go 1.25.0

require github.com/bonyai/tyto-go v0.0.0

require (
	buf.build/gen/go/bonya/tyto/grpc/go v1.6.2-20260817064545-41ffb38cb586.1 // indirect
	buf.build/gen/go/bonya/tyto/protocolbuffers/go v1.36.12-20260817064545-41ffb38cb586.1 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
	google.golang.org/grpc v1.82.1 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/bonyai/tyto-go => ..
