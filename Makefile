#
# Common
#

.PHONY: help
help:
	@awk 'BEGIN { \
		FS = ":.*##"; \
		printf "\nUsage:\n  make \033[36m<target>\033[0m\n" \
	} \
	/^[a-zA-Z_-]+:.*?##/ { \
		printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2 \
	} \
	/^##@/ { \
		printf "\n\033[1m%s\033[0m\n", substr($$0, 5) \
	}' $(MAKEFILE_LIST)

.DEFAULT_GOAL := help

#
# Private Jenkins (commands below are used for releases/private Jenkins)
#

##@ Release/Private Jenkins/Build

export BRANCH_NAME ?= $(shell git rev-parse --abbrev-ref HEAD | sed "s!/!-!g")

ifeq (${BRANCH_NAME},main)
	export TAG := $(shell git rev-parse --short HEAD)-go${GO_VERSION}
	export BRANCH_NAME :=
else
	export TAG := $(shell git rev-parse --short HEAD)-${BRANCH_NAME}-go${GO_VERSION}
	ifneq ($(shell git describe --tags --exact-match --match "v[0-9]*\.[0-9]*\.[0-9]*"),)
		export LATEST_STABLE_TAG := latest
	endif
endif

export LATEST_DEV_TAG := dev

.PHONY: images
images: storjscan-image ## Build Docker images
	@echo Built version: ${TAG}

.PHONY: storjscan-image
storjscan-image: ## Build storjscan Docker image
	docker bake -f docker-bake.hcl image

.PHONY: binaries
binaries: ## Build storjscan binaries
	docker bake -f docker-bake.hcl binaries

.PHONY: push-images
push-images: ## Push Docker images to Docker Hub
	docker bake -f docker-bake.hcl image --push

.PHONY: compress-binaries
compress-binaries: ## Compress release binaries for uploading
	./scripts/release/compress-binaries.sh "release/${TAG}"

.PHONY: binaries-upload
binaries-upload: ## Upload release binaries to GCS
	cd "release/${TAG}"; gsutil -m cp -r *.zip "gs://storj-v3-alpha-builds/${TAG}/"

##@ Release/Private Jenkins/Clean

.PHONY: clean
clean: clean-binaries clean-images ## Remove local release binaries and local Docker images

.PHONY: clean-binaries
clean-binaries: ## Remove local release binaries
	rm -rf release

.PHONY: clean-images
clean-images:
	-docker rmi -f $(shell docker images -q "storjlabs/storjscan:${TAG}-*")
