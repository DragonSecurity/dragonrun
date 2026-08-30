build:
  go build -o bin/dragonrun .

init: build
  ./bin/dragonrun init

test:
  go test ./...

fmt:
  gofmt -w cmd internal main.go

vet:
  go vet ./...

check: fmt vet test

up:
  go run . up

down:
  go run . down

status:
  go run . status

logs *args:
  go run . logs {{args}}

# Re-provision everything from registry.json after editing it by hand.
sync:
  go run . sync

# Preview which repos are ready to have their compose files deleted.
survey-tidy dir:
  #!/usr/bin/env bash
  for d in {{dir}}/*/; do
    [ -f "$d/docker-compose.yml" ] || continue
    go run . tidy "$d" 2>&1 | head -20
    echo
  done

# Preview what adopting every repo under a directory would do.
survey dir:
  #!/usr/bin/env bash
  for d in {{dir}}/*/; do
    [ -f "$d/docker-compose.yml" ] || continue
    go run . adopt --dry-run "$d" 2>/dev/null | head -6
    echo
  done

# Validate .goreleaser.yml and build every target locally, without releasing.
release-check:
  goreleaser check
  goreleaser build --snapshot --clean

release:
  goreleaser build --snapshot --clean
