BINARY := email-fetcher
CMD    := ./cmd/server
BIN    := ./bin

.PHONY: build build-darwin-amd64 test vet proto clean

build:
	go build -o $(BIN)/$(BINARY) $(CMD)

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build -o $(BIN)/$(BINARY)-darwin-amd64 $(CMD)

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

proto:
	protoc --go_out=gen/go --go_opt=paths=source_relative \
		--go-grpc_out=gen/go --go-grpc_opt=paths=source_relative \
		--proto_path=proto \
		proto/email/v1/email.proto

clean:
	rm -rf $(BIN)
