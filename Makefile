.PHONY: all lint test test-coverage build docker-build docker-build-backend docker-build-web docker-up docker-down docker-logs docker-clean up-db down-db clean

ENV_PATH := PATH="$(HOME)/.goenv/shims:/opt/homebrew/bin:/usr/local/bin:$(PATH)"
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "latest")
GIT_DIRTY  ?= $(shell git diff --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null || echo "-dirty")
IMAGE_TAG  ?= $(GIT_COMMIT)$(GIT_DIRTY)

BACKEND_IMAGE ?= selfcloud-backend:$(IMAGE_TAG)
WEB_IMAGE     ?= selfcloud-web:$(IMAGE_TAG)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -X main.buildVersion=$(IMAGE_TAG) -X main.buildCommit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)

all: lint test build

lint:
	$(ENV_PATH) golangci-lint run ./...

test:
	$(ENV_PATH) go test -v ./...

test-coverage:
	$(ENV_PATH) go test -coverprofile=coverage.out ./...
	$(ENV_PATH) go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"
	@open coverage.html

build:
	$(ENV_PATH) go build -ldflags="$(LDFLAGS)" -o bin/selfcloud ./cmd/selfcloud

docker-build-backend:
	docker build --build-arg IMAGE_TAG=$(IMAGE_TAG) -t $(BACKEND_IMAGE) -t selfcloud-backend:latest -f Dockerfile .

docker-build-web:
	docker build --build-arg IMAGE_TAG=$(IMAGE_TAG) -t $(WEB_IMAGE) -t selfcloud-web:latest -f Dockerfile.web .

docker-build: docker-build-backend docker-build-web

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

docker-clean:
	docker compose down -v

up-db:
	docker compose up -d mariadb

down-db:
	docker compose down -v

ui-install:
	$(MAKE) -C web install

ui-build:
	$(MAKE) -C web build

ui-dev:
	$(MAKE) -C web dev

clean:
	rm -rf bin/
	$(MAKE) -C web clean

INVENTORY ?= sample
ANSIBLE_USER ?=
ANSIBLE_USER_FLAG := $(if $(ANSIBLE_USER),-u $(ANSIBLE_USER),)

docker-export:
	@mkdir -p /tmp/selfcloud-build
	@if [ "$(GIT_DIRTY)" = "-dirty" ] || [ ! -f /tmp/selfcloud-build/selfcloud-$(IMAGE_TAG).tar.gz ]; then \
		echo "Building and exporting images for tag $(IMAGE_TAG)..."; \
		$(MAKE) docker-build; \
		docker save $(BACKEND_IMAGE) $(WEB_IMAGE) | gzip -n > /tmp/selfcloud-build/selfcloud-$(IMAGE_TAG).tar.gz; \
	else \
		echo "Images for $(IMAGE_TAG) already exported at /tmp/selfcloud-build/selfcloud-$(IMAGE_TAG).tar.gz (clean git commit, skipping build)"; \
	fi

deploy-ansible: docker-export
	ansible-playbook -i deploy/inventories.local/$(INVENTORY)/inventory.ini deploy/playbook.yml -e image_tag=$(IMAGE_TAG) $(ANSIBLE_USER_FLAG)

