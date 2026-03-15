BINARY = synapse
VERSION = 0.7.0
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build windows linux clean test

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/synapse

windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY).exe ./cmd/synapse

linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(BINARY)-linux ./cmd/synapse

all: build windows linux

clean:
	rm -f $(BINARY) $(BINARY).exe $(BINARY)-linux

test:
	go test ./...

install: build
	cp $(BINARY) /usr/local/bin/
