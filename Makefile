.PHONY: dev up down m0-verify m1-verify m2-verify m6-verify m7-verify m8-verify m9-verify backend-test backend-integration

dev:
	npm run dev

up:
	docker compose up --build

down:
	docker compose down

backend-test:
	cd server && go test -race ./... && go vet ./...

backend-integration:
	cd server && go test -p=1 -tags=integration ./internal/database ./internal/auth ./internal/scm ./internal/approvals ./internal/repair ./internal/automations

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

m2-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.scm-smoke.yaml config --quiet

m6-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.scm-smoke.yaml -f compose.m6-smoke.yaml config --quiet

m7-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.scm-smoke.yaml -f compose.m6-smoke.yaml -f compose.m7-smoke.yaml config --quiet

m8-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.scm-smoke.yaml -f compose.m6-smoke.yaml -f compose.m7-smoke.yaml -f compose.m8-smoke.yaml config --quiet

m9-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.scm-smoke.yaml -f compose.m6-smoke.yaml -f compose.m7-smoke.yaml -f compose.m8-smoke.yaml -f compose.m9-smoke.yaml config --quiet
