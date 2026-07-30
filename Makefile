.PHONY: dev up down m0-verify backend-test backend-integration

dev:
	npm run dev

up:
	docker compose up --build

down:
	docker compose down

backend-test:
	cd server && go test -race ./... && go vet ./...

backend-integration:
	cd server && go test -tags=integration ./internal/database

m0-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
