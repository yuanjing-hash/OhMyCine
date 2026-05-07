# OhMyCine Development Makefile

.PHONY: help player-dev player-build player-lint player-typecheck \
       server-dev server-build server-test server-lint \
       cli-build cli-test cli-lint \
       hub-dev hub-build \
       lint test build dev clean

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ====================
# Player
# ====================

player-dev: ## Player 开发模式
	cd player && npm run tauri dev

player-build: ## Player 构建
	cd player && npm run build

player-lint: ## Player Lint
	cd player && npm run lint

player-typecheck: ## Player 类型检查
	cd player && npm run typecheck

player-install: ## Player 安装依赖
	cd player && npm install

# ====================
# Server
# ====================

server-dev: ## Server 开发模式
	cd server && go run ./cmd/server

server-build: ## Server 构建
	cd server && go build -o bin/ohmycine-server ./cmd/server

server-test: ## Server 测试
	cd server && go test ./...

server-lint: ## Server Lint
	cd server && golangci-lint run

# ====================
# CLI
# ====================

cli-build: ## CLI 构建
	cd cli && go build -o bin/omc ./cmd/omc

cli-test: ## CLI 测试
	cd cli && go test ./...

cli-lint: ## CLI Lint
	cd cli && golangci-lint run

# ====================
# Hub
# ====================

hub-dev: ## Hub 开发模式
	cd hub && npm run dev

hub-build: ## Hub 构建
	cd hub && npm run build

hub-install: ## Hub 安装依赖
	cd hub && npm install

# ====================
# Aggregate
# ====================

lint: player-lint server-lint cli-lint ## 全量 Lint

test: server-test cli-test ## 全量测试

build: player-build server-build cli-build hub-build ## 全量构建

dev: player-dev ## 启动开发 (Player)

clean: ## 清理构建产物
	rm -rf player/dist player/src-tauri/target
	rm -rf server/bin
	rm -rf cli/bin
	rm -rf hub/.vitepress/dist hub/.vitepress/cache
