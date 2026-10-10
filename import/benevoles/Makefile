# Development shortcuts. The application itself needs nothing but `go`.

.PHONY: dev test build

# Run a local instance: e-mails, login links included, are printed on the
# console instead of being sent. The database is ./benevoles.db, created on
# first start; delete it to start over. Another port:
#   ADDR=:8081 BASE_URL=http://localhost:8081 make dev
dev:
	DEV_MODE=1 go run . serve

test:
	go vet ./...
	go test ./...

build:
	go build -o benevoles .
