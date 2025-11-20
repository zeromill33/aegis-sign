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
	$(GO) test ./pkg/signerapi -run TestCreateFastPathBudget -count=1
	$(GO) test ./pkg/... ./docs/api/tests
