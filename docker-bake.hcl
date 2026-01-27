// TAG is the Docker image tag suffix used for published images.
// Example: "v1.120.8" for releases, "dev" for development builds.
variable "TAG" {
  default = "dev"
}

// LATEST_STABLE_TAG controls whether to also tag images as "latest".
// Set to "-latest" to add the latest tag, or "" to skip it.
variable "LATEST_STABLE_TAG" {
  default = ""
}

// LATEST_DEV_TAG controls whether to also tag images as "dev".
// Set to "-dev" to add the latest tag, or "" to skip it.
variable "LATEST_DEV_TAG" {
  default = "dev"
}

// binaries target does a cross-compilation of all binaries.
target "binaries" {
  target = "export-binaries"
  dockerfile = "release.Dockerfile"
  dockerignore = "release.Dockerfile.dockerignore"

  output = ["type=local,dest=./release/${TAG}/"]
}

// image_tags is a function that returns a list of image tags for a given image name.
// It automatically adds "latest" tag when LATEST_TAG is not empty.
function "image_tags" {
  params = [name]
  result = LATEST_STABLE_TAG != "" ? [
    "storjlabs/${name}:${TAG}",
    "storjlabs/${name}:${LATEST_DEV_TAG}",
    "storjlabs/${name}:${LATEST_STABLE_TAG}"
  ] : [
    "storjlabs/${name}:${TAG}",
    "storjlabs/${name}:${LATEST_DEV_TAG}",
  ]
}

target "image" {
  dockerfile = "cmd/storjscan/Dockerfile"
  context   = "."
  platforms = ["linux/amd64", "linux/arm64"]
  contexts  = {
    binaries = "target:binaries"
  }
  pull = true
  tags = image_tags("storjscan")
}
