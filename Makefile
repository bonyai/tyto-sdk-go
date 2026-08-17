.PHONY: proto test build vet examples check

# There is no local codegen step. The generated protobuf and gRPC code comes
# from the Buf Schema Registry as ordinary Go modules:
#
#   buf.build/gen/go/bonya/tyto/protocolbuffers/go   messages (runtimev1)
#   buf.build/gen/go/bonya/tyto/grpc/go              service stubs (runtimev1grpc)
#
# To pick up a new schema, publish it (see ../../bsr/README.md) and then bump
# the two modules here:
#
#   go get buf.build/gen/go/bonya/tyto/protocolbuffers/go@latest \
#          buf.build/gen/go/bonya/tyto/grpc/go@latest
#
# BSR generates these on demand, so a just-published version can be missing
# from proxy.golang.org's cache for a short while. If `go get` resolves the
# previous version, fetch through the BSR proxy directly:
#
#   GOPROXY=https://buf.build/gen/go,https://proxy.golang.org,direct GOSUMDB=off go get ...
proto:
	@echo "No local codegen. See the comment above this target for how to" >&2
	@echo "pick up a new schema version from the BSR." >&2
	@exit 1

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

# examples/ is its own module (so `go get` on this SDK never pulls it in), and
# therefore is not covered by ./... above -- it needs building separately or a
# broken example ships unnoticed.
examples:
	cd examples && go build ./... && go vet ./...

check: vet test examples
