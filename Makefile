.PHONY: dev up down m0-verify m1-verify m2-verify m6-verify m7-verify m8-verify m9-verify m10-verify backend-test backend-integration backend-coverage ops-test backup restore

dev:
	npm run dev

up:
	docker compose up --build

down:
	docker compose down

backend-test:
	cd server && go test -race -count=1 ./... && go vet ./...

backend-integration:
	cd server && go test -count=1 -p=1 -tags=integration ./internal/database ./internal/auth ./internal/scm ./internal/tasks ./internal/approvals ./internal/repair ./internal/automations ./internal/retention

backend-coverage:
	cd server && go test -count=1 -covermode=atomic -coverprofile=coverage.out ./... && ../scripts/check-go-coverage.sh coverage.out

ops-test:
	bash -n scripts/backup.sh scripts/restore.sh scripts/check-go-coverage.sh scripts/ops-regression.sh
	./scripts/ops-regression.sh

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

m10-verify:
	npm run lint
	npm run typecheck
	npm test
	$(MAKE) backend-test
	cd server && go test -run 'Test(Middleware|Limiter|Traceparent|Cutoff|M10)' ./internal/metrics ./internal/ratelimit ./internal/telemetry ./internal/retention
	bash -n scripts/backup.sh scripts/restore.sh
	docker compose config --quiet
	docker compose -f compose.yaml -f compose.scm-smoke.yaml -f compose.m6-smoke.yaml -f compose.m7-smoke.yaml -f compose.m8-smoke.yaml -f compose.m9-smoke.yaml -f compose.m10-smoke.yaml config --quiet

backup:
	@test -n "$(BACKUP_FILE)" || (echo 'set BACKUP_FILE=/absolute/path/to/backup.dump' && exit 2)
	REPOMENDER_DATABASE_URL="$(REPOMENDER_DATABASE_URL)" BACKUP_FILE="$(BACKUP_FILE)" FORCE="$(FORCE)" ./scripts/backup.sh

restore:
	@test -n "$(BACKUP_FILE)" || (echo 'set BACKUP_FILE=/absolute/path/to/backup.dump' && exit 2)
	REPOMENDER_DATABASE_URL="$(REPOMENDER_DATABASE_URL)" BACKUP_FILE="$(BACKUP_FILE)" CONFIRM_RESTORE="$(CONFIRM_RESTORE)" ./scripts/restore.sh
