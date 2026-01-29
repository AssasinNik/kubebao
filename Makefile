# KubeBao Makefile

# Variables
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
REGISTRY ?= ghcr.io
IMAGE_PREFIX ?= kubebao
GO_VERSION ?= 1.23
PLATFORMS ?= linux/amd64,linux/arm64

# Go settings
GOFLAGS ?= -mod=readonly
LDFLAGS := -s -w -X main.Version=$(VERSION)

# Colors
CYAN := \033[36m
GREEN := \033[32m
YELLOW := \033[33m
RESET := \033[0m

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\n$(CYAN)Usage:$(RESET)\n  make $(GREEN)<target>$(RESET)\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  $(GREEN)%-15s$(RESET) %s\n", $$1, $$2 } /^##@/ { printf "\n$(YELLOW)%s$(RESET)\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Format Go code
	@echo "$(CYAN)Formatting code...$(RESET)"
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	@echo "$(CYAN)Running go vet...$(RESET)"
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	@echo "$(CYAN)Running linter...$(RESET)"
	golangci-lint run ./...

.PHONY: test
test: ## Run unit tests
	@echo "$(CYAN)Running tests...$(RESET)"
	go test -v -race -coverprofile=coverage.out ./...

.PHONY: test-e2e
test-e2e: ## Run E2E tests
	@echo "$(CYAN)Running E2E tests...$(RESET)"
	./scripts/e2e-test.sh

.PHONY: test-e2e-quick
test-e2e-quick: ## Run quick E2E tests
	@echo "$(CYAN)Running quick E2E tests...$(RESET)"
	./scripts/e2e-test.sh --quick

.PHONY: coverage
coverage: test ## Generate coverage report
	@echo "$(CYAN)Generating coverage report...$(RESET)"
	go tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)Coverage report: coverage.html$(RESET)"

##@ Build

.PHONY: build
build: build-kms build-csi build-operator ## Build all binaries

.PHONY: build-kms
build-kms: ## Build KMS plugin
	@echo "$(CYAN)Building kubebao-kms...$(RESET)"
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/kubebao-kms ./cmd/kubebao-kms

.PHONY: build-csi
build-csi: ## Build CSI provider
	@echo "$(CYAN)Building kubebao-csi...$(RESET)"
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/kubebao-csi ./cmd/kubebao-csi

.PHONY: build-operator
build-operator: ## Build operator
	@echo "$(CYAN)Building kubebao-operator...$(RESET)"
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/kubebao-operator ./cmd/kubebao-operator

##@ Docker

.PHONY: docker-build
docker-build: docker-build-kms docker-build-csi docker-build-operator ## Build all Docker images

.PHONY: docker-build-kms
docker-build-kms: ## Build KMS Docker image
	@echo "$(CYAN)Building kubebao-kms image...$(RESET)"
	docker build -t $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-kms:$(VERSION) \
		--build-arg COMPONENT=kubebao-kms \
		--build-arg VERSION=$(VERSION) .

.PHONY: docker-build-csi
docker-build-csi: ## Build CSI Docker image
	@echo "$(CYAN)Building kubebao-csi image...$(RESET)"
	docker build -t $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-csi:$(VERSION) \
		--build-arg COMPONENT=kubebao-csi \
		--build-arg VERSION=$(VERSION) .

.PHONY: docker-build-operator
docker-build-operator: ## Build operator Docker image
	@echo "$(CYAN)Building kubebao-operator image...$(RESET)"
	docker build -t $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-operator:$(VERSION) \
		--build-arg COMPONENT=kubebao-operator \
		--build-arg VERSION=$(VERSION) .

.PHONY: docker-push
docker-push: ## Push Docker images
	@echo "$(CYAN)Pushing Docker images...$(RESET)"
	docker push $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-kms:$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-csi:$(VERSION)
	docker push $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-operator:$(VERSION)

.PHONY: docker-buildx
docker-buildx: ## Build multi-arch Docker images
	@echo "$(CYAN)Building multi-arch images...$(RESET)"
	docker buildx build --platform $(PLATFORMS) \
		-t $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-kms:$(VERSION) \
		--build-arg COMPONENT=kubebao-kms --push .
	docker buildx build --platform $(PLATFORMS) \
		-t $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-csi:$(VERSION) \
		--build-arg COMPONENT=kubebao-csi --push .
	docker buildx build --platform $(PLATFORMS) \
		-t $(REGISTRY)/$(IMAGE_PREFIX)/kubebao-operator:$(VERSION) \
		--build-arg COMPONENT=kubebao-operator --push .

##@ Helm

.PHONY: helm-lint
helm-lint: ## Lint Helm chart
	@echo "$(CYAN)Linting Helm chart...$(RESET)"
	helm lint ./charts/kubebao

.PHONY: helm-package
helm-package: ## Package Helm chart
	@echo "$(CYAN)Packaging Helm chart...$(RESET)"
	helm package ./charts/kubebao --destination .deploy

.PHONY: helm-install
helm-install: ## Install Helm chart locally
	@echo "$(CYAN)Installing KubeBao...$(RESET)"
	helm upgrade --install kubebao ./charts/kubebao \
		--namespace kubebao-system --create-namespace \
		--set global.image.tag=$(VERSION) \
		--set global.image.pullPolicy=Never

.PHONY: helm-uninstall
helm-uninstall: ## Uninstall Helm chart
	@echo "$(CYAN)Uninstalling KubeBao...$(RESET)"
	helm uninstall kubebao -n kubebao-system || true

##@ CRDs

.PHONY: manifests
manifests: ## Generate CRD manifests
	@echo "$(CYAN)Generating CRD manifests...$(RESET)"
	controller-gen crd paths="./api/..." output:crd:dir=config/crd

.PHONY: install-crds
install-crds: ## Install CRDs
	@echo "$(CYAN)Installing CRDs...$(RESET)"
	kubectl apply -f config/crd/

.PHONY: uninstall-crds
uninstall-crds: ## Uninstall CRDs
	@echo "$(CYAN)Uninstalling CRDs...$(RESET)"
	kubectl delete -f config/crd/ --ignore-not-found

##@ Local Development

.PHONY: setup
setup: ## Full local setup
	@echo "$(CYAN)Running full setup...$(RESET)"
	./scripts/setup-all.sh

.PHONY: cleanup
cleanup: ## Clean up local environment
	@echo "$(CYAN)Cleaning up...$(RESET)"
	./scripts/cleanup.sh --force

.PHONY: run-operator
run-operator: ## Run operator locally
	@echo "$(CYAN)Running operator locally...$(RESET)"
	go run ./cmd/kubebao-operator

##@ Dependencies

.PHONY: deps
deps: ## Download dependencies
	@echo "$(CYAN)Downloading dependencies...$(RESET)"
	go mod download

.PHONY: tidy
tidy: ## Tidy dependencies
	@echo "$(CYAN)Tidying dependencies...$(RESET)"
	go mod tidy

.PHONY: verify
verify: ## Verify dependencies
	@echo "$(CYAN)Verifying dependencies...$(RESET)"
	go mod verify

##@ Clean

.PHONY: clean
clean: ## Clean build artifacts
	@echo "$(CYAN)Cleaning...$(RESET)"
	rm -rf bin/
	rm -rf .deploy/
	rm -f coverage.out coverage.html

.PHONY: clean-all
clean-all: clean ## Clean everything including vendor
	rm -rf vendor/
