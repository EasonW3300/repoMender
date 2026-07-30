.PHONY: dev up down m0-verify m1-verify backend-test backend-integration

dev:
	npm run dev

up:
	docker compose up --build

down:
	docker compose down

backend-test:
	cd server && go test -race ./... && go vet ./...

backend-integration:
	cd server && go test -tags=integration ./internal/database ./internal/auth

m0-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet

m1-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.oidc-smoke.yaml config --quiet
