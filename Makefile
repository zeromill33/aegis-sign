.PHONY: test api-contract api-ci

GO ?= /opt/homebrew/bin/go

# 运行所有 Go 测试（validator/apierrors/schema）
test:
	$(GO) test ./...

# API 合约校验，仅运行与 API 相关的用例
api-contract:
	$(GO) test ./pkg/... ./docs/api/tests

# API CI：lint + 生成 + SLA 验证
api-ci:
	buf lint
	buf generate --template buf.gen.yaml
	@if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then \
	  if git rev-parse HEAD >/dev/null 2>&1; then \
	    git diff --quiet -- docs/api/gen/go || (echo "proto outputs not up-to-date" && exit 1); \
	  fi; \
	fi
	$(GO) test ./internal/api -run TestCreateFastPathBudget -count=1
	$(GO) test ./pkg/... ./internal/... ./docs/api/tests

.PHONY: conn-pool-test
conn-pool-test:
	$(GO) test ./internal/infra/enclaveclient -run TestConnPoolRace -race -count=1
	$(GO) test ./internal/infra/enclaveclient ./internal/api
	@if command -v golangci-lint >/dev/null 2>&1; then \
	  golangci-lint run ./pkg/... ; \
	else \
	  echo "golangci-lint not installed, skipping lint"; \
	fi
	@if command -v ghz >/dev/null 2>&1; then \
	  echo "running ghz smoke" && ghz --help >/dev/null; \
	else \
	  echo "ghz not installed - follow docs/bench/s2-long-connection.md"; \
	fi

.PHONY: unlock-drill
unlock-drill:
	@echo "[unlock] running dispatcher/kms drills"
	$(GO) test ./internal/gateway/unlock -run TestDispatcherRetriesUpToMaxAttempts -count=1
	$(GO) test ./internal/infra/kms -run TestClientRetriesUntilSuccess -count=1
	@ts=$$(date -u '+%Y-%m-%dT%H:%M:%SZ'); \
	  mkdir -p docs/bench/reports; \
	  { \
	    echo "# Unlock Drill Report"; \
	    echo "- Timestamp: $$ts"; \
	    echo "- Tests: internal/gateway/unlock, internal/infra/kms"; \
	    echo "- Next Steps: 1) ghz -d @docs/bench/payloads/unlock.json ... 2) 观察 unlock_* 指标 3) 更新 Grafana 截图"; \
	  } > docs/bench/reports/unlock-drill.md; \
	  echo "unlock drill report written to docs/bench/reports/unlock-drill.md"
