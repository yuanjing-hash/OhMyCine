.PHONY: help install dev typecheck lint test build clean

help:
	@echo "install typecheck lint test build dev clean"

install:
	npm install

dev:
	npm run tauri:dev:windows

typecheck:
	npm run typecheck

lint:
	npm run lint

test:
	npm run typecheck
	npm run lint

build:
	npm run build

clean:
	git clean -fdX dist src-tauri/target
