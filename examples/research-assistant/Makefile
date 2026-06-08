# Image build helpers for the research-assistant example.
#
# One image runs both agents; the AgentSet picks the role with `-role`.
#
# Usage:
#   make build                          # local docker build
#   make IMAGE_REPO=quay.io/kynoio/examples/research-assistant-go push
#   make IMAGE_REPO=quay.io/kynoio/examples/research-assistant-go buildx-push  # multi-arch push

IMAGE_REPO    ?= quay.io/kynoio/examples/research-assistant-go
IMAGE_TAG     ?= latest
IMAGE         := $(IMAGE_REPO):$(IMAGE_TAG)
PLATFORMS     ?= linux/amd64,linux/arm64

# Repo root — Docker build context must include go.mod.
REPO_ROOT     := $(abspath $(CURDIR)/../..)
DOCKERFILE    := $(CURDIR)/Dockerfile

.PHONY: all
all: build

.PHONY: build
build: ## Build the image for the host platform.
	docker build \
		-f $(DOCKERFILE) \
		-t $(IMAGE) \
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
