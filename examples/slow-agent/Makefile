# Image build helpers for the slow-agent example.
#
# A single agent whose handler holds each request open for a fixed delay
# (-delay / SLOW_AGENT_DELAY), used to drive autoscaling load in e2e tests.
#
# Usage:
#   make build                          # local docker build
#   make IMAGE_REPO=quay.io/kynoio/examples/slow-agent-go push
#   make IMAGE_REPO=quay.io/kynoio/examples/slow-agent-go buildx-push  # multi-arch push

IMAGE_REPO    ?= quay.io/kynoio/examples/slow-agent-go
IMAGE_TAG     ?= latest
IMAGE         := $(IMAGE_REPO):$(IMAGE_TAG)
PLATFORMS     ?= linux/amd64,linux/arm64

# Repo root — Docker build context must include go.mod.
REPO_ROOT     := $(abspath $(CURDIR)/../..)
DOCKERFILE    := $(CURDIR)/Dockerfile

# Optional: point CA_CERT at a corporate proxy CA (e.g. Netskope) so module
# fetches over an intercepted TLS connection verify inside the builder. When
# set, its directory is exposed to the build as the `cacert` context.
CA_CERT       ?=
ifneq ($(CA_CERT),)
CACERT_ARG    := --build-context cacert=$(dir $(CA_CERT))
endif

.PHONY: all
all: build

.PHONY: build
build: ## Build the image for the host platform.
	docker build \
		-f $(DOCKERFILE) \
		-t $(IMAGE) \
		$(CACERT_ARG) \
		$(REPO_ROOT)

.PHONY: push
push: build ## Build and push for the host platform.
	docker push $(IMAGE)

.PHONY: buildx-push
buildx-push: ## Build and push a multi-arch manifest ($(PLATFORMS)).
	docker buildx build \
		--platform $(PLATFORMS) \
		-f $(DOCKERFILE) \
		-t $(IMAGE) \
		$(CACERT_ARG) \
		--push \
		$(REPO_ROOT)

.PHONY: clean
clean: ## Remove the locally-built image.
	-docker image rm $(IMAGE)

.PHONY: print
print: ## Show resolved variables.
	@echo "IMAGE=$(IMAGE)"
	@echo "PLATFORMS=$(PLATFORMS)"
	@echo "REPO_ROOT=$(REPO_ROOT)"

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
