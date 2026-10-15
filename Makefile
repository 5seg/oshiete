# workerd ships as a glibc binary and segfaults under musl, which takes
# `cf build`, `cf dev` and `cf workers types` with it. scripts/cf runs the CLI in
# a glibc container so the commands and config files stay the same.
CF := ./scripts/cf

.DEFAULT_GOAL := help
.PHONY: help tui sync sites dev deploy migrate types check seed clean

help: ## list targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-9s %s\n", $$1, $$2}'

tui: ## build the terminal dashboard
	cd tui && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o oshiete .

check: ## vet the Go code and typecheck the Worker
	cd tui && go vet ./...
	$(MAKE) types
	cd worker && npx --yes tsc

types: ## regenerate Worker binding types from cloudflare.config.ts
	$(CF) workers types

dev: ## run the Worker locally
	$(CF) dev

deploy: ## build and deploy the collector
	$(CF) deploy --secrets-file .secrets.json

migrate: ## apply pending D1 migrations (DB_ID=c70f5fd2-... )
	$(CF) d1 migrations apply $(DB_ID) --dir migrations

sync: ## pull new events into the local mirror
	./tui/oshiete sync

sites: ## list sites in the local mirror
	./tui/oshiete sites

seed: ## fill a throwaway mirror with demo traffic
	python3 scripts/seed.py ~/oshiete/data/demo.db --force
	@echo "OSHIETE_DB=$$HOME/oshiete/data/demo.db ./tui/oshiete"

clean: ## remove build output and local mirrors
	rm -f tui/oshiete data/mirror.db data/mirror.db-* data/demo.db
	rm -rf worker/.cloudflare worker/dist
