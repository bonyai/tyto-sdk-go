.PHONY: proto proto-export test build vet examples check

# Proto source module on the Buf Schema Registry. `proto` exports this
# module's current contents into PROTO_EXPORT_DIR before generating, so this
# SDK never needs a local checkout of the compute repository.
BSR_MODULE ?= buf.build/bonya/tyto
PROTO_EXPORT_DIR ?= .proto-export
BUF ?= buf

# Override to generate from a local checkout instead of BSR, e.g. while
# developing against unpublished proto changes:
#   make proto PROTO_DIR=../../compute/proto
PROTO_DIR ?= $(PROTO_EXPORT_DIR)

PROTOC ?= protoc
GOBIN ?= $(shell go env GOPATH)/bin

proto-export:
	rm -rf $(PROTO_EXPORT_DIR)
	$(BUF) export $(BSR_MODULE) -o $(PROTO_EXPORT_DIR)

ifeq ($(PROTO_DIR),$(PROTO_EXPORT_DIR))
proto: proto-export
endif
proto:
	PATH="$(GOBIN):$$PATH" $(PROTOC) \
		--proto_path=$(PROTO_DIR) \
		--go_out=internal/gen --go_opt=paths=source_relative \
		--go-grpc_out=internal/gen --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/tyto/runtime/v1/guest.proto \
		$(PROTO_DIR)/tyto/runtime/v1/host.proto \
		$(PROTO_DIR)/tyto/runtime/v1/preview.proto \
		$(PROTO_DIR)/tyto/runtime/v1/tapi.proto

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
