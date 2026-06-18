BINARY := agistry

.PHONY: build test tidy run clean

build:
	CGO_ENABLED=0 go build -buildvcs=false -o $(BINARY) .

test:
	go test ./...

tidy:
	go mod tidy

run: build
	./$(BINARY)

clean:
	rm -f $(BINARY) registry.db registry.db-wal registry.db-shm
