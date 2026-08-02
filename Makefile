BIN := bin

.PHONY: build test vet clean sim

build:
	mkdir -p $(BIN)
	go build -o $(BIN)/rvf-habridge ./cmd/rvf-habridge
	go build -o $(BIN)/rvf-simulator ./cmd/rvf-simulator

test:
	go test ./...

vet:
	go vet ./...

sim: build
	$(BIN)/rvf-simulator --listen tcp://127.0.0.1:5502 --idus 5

clean:
	rm -rf $(BIN)
