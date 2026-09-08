.PHONY: build test vet integration image clean

build:
	go build -o bin/pgstress ./cmd/pgstress

test:
	go test -race ./...

vet:
	go vet ./...

# Needs a reachable Postgres, e.g.:
#   docker run -d --rm -p 5432:5432 -e POSTGRES_HOST_AUTH_METHOD=trust postgres:18
#   PGHOST=localhost PGUSER=postgres make integration
integration:
	go test -race -tags integration -run Integration -v ./cmd/pgstress/

image:
	./run.sh build

clean:
	rm -rf bin out
