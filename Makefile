COMPONENTLIST := storjscan

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

GO_VERSION ?= 1.18.8
BRANCH_NAME ?= $(shell git rev-parse --abbrev-ref HEAD | sed "s!/!-!g")

ifeq (${BRANCH_NAME},main)
	TAG := $(shell git rev-parse --short HEAD)-go${GO_VERSION}
	BRANCH_NAME :=
else
	TAG := $(shell git rev-parse --short HEAD)-${BRANCH_NAME}-go${GO_VERSION}
	ifneq ($(shell git describe --tags --exact-match --match "v[0-9]*\.[0-9]*\.[0-9]*"),)
		LATEST_STABLE_TAG := latest
	endif
endif

DOCKER_BUILD := docker build --build-arg TAG=${TAG}

LATEST_DEV_TAG := dev

.PHONY: images
images: storjscan-image ## Build Docker images
	@echo Built version: ${TAG}

.PHONY: storjscan-image
storjscan-image: ## Build storjscan Docker image
	${DOCKER_BUILD} --pull=true -t storjlabs/storjscan:${TAG}-amd64 \
		-f cmd/storjscan/Dockerfile .
	${DOCKER_BUILD} --pull=true -t storjlabs/storjscan:${TAG}-arm32v6 \
		--build-arg=GOARCH=arm \
		--build-arg=DOCKER_ARCH=arm32v6 \
		-f cmd/storjscan/Dockerfile .
	${DOCKER_BUILD} --pull=true -t storjlabs/storjscan:${TAG}-arm64v8 \
		--build-arg=GOARCH=arm64 \
		--build-arg=DOCKER_ARCH=arm64v8 \
		-f cmd/storjscan/Dockerfile .
	docker tag storjlabs/storjscan:${TAG}-amd64 storjlabs/storjscan:${LATEST_DEV_TAG}

.PHONY: binaries
binaries: ${BINARIES} ## Build storjscan binaries
	for C in ${COMPONENTLIST}; do \
		CGO_ENABLED=0 storj-release \
			--components "cmd/$$C" \
			--build-tags kqueue \
			--go-version "${GO_VERSION}" \
			--branch "${BRANCH_NAME}" \
			--skip-osarches "freebsd/amd64" || exit $$? \
	; done

.PHONY: push-images
push-images: ## Push Docker images to Docker Hub
	# images have to be pushed before a manifest can be created
	for c in ${COMPONENTLIST}; do \
		docker push storjlabs/$$c:${TAG}-amd64 \
		&& docker push storjlabs/$$c:${TAG}-arm32v6 \
		&& docker push storjlabs/$$c:${TAG}-arm64v8 \
		&& for t in ${TAG} ${LATEST_DEV_TAG} ${LATEST_STABLE_TAG}; do \
			docker manifest create storjlabs/$$c:$$t \
			storjlabs/$$c:${TAG}-amd64 \
			storjlabs/$$c:${TAG}-arm32v6 \
			storjlabs/$$c:${TAG}-arm64v8 \
			&& docker manifest annotate storjlabs/$$c:$$t storjlabs/$$c:${TAG}-amd64 --os linux --arch amd64 \
			&& docker manifest annotate storjlabs/$$c:$$t storjlabs/$$c:${TAG}-arm32v6 --os linux --arch arm --variant v6 \
			&& docker manifest annotate storjlabs/$$c:$$t storjlabs/$$c:${TAG}-arm64v8 --os linux --arch arm64 --variant v8 \
			&& docker manifest push --purge storjlabs/$$c:$$t \
		; done \
	; done

.PHONY: binaries-upload
binaries-upload: ## Upload release binaries to GCS
	cd "release/${TAG}"; for f in *; do \
		c="$${f%%_*}" \
		&& if [ "$${f##*.}" != "$${f}" ]; then \
			ln -s "$${f}" "$${f%%_*}.$${f##*.}" \
			&& zip "$${f}.zip" "$${f%%_*}.$${f##*.}" \
			&& rm "$${f%%_*}.$${f##*.}" \
		; else \
			ln -sf "$${f}" "$${f%%_*}" \
			&& zip "$${f}.zip" "$${f%%_*}" \
			&& rm "$${f%%_*}" \
		; fi \
	; done
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
