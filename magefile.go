// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

//go:build mage
// +build mage

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
	"github.com/zeebo/errs"
)

// Test executes all unit and integration tests.
//nolint:deadcode
func Test() error {
	err := sh.RunV("go", "test", "./...")
	return err
}

// Coverage executes all unit test with coverage measurement.
//nolint:deadcode
func Coverage() error {
	fmt.Println("Executing tests and generate coverage information")
	err := sh.RunV("go", "test", "-coverprofile=./tmp/coverage.out", "./...")
	if err != nil {
		return err
	}
	return sh.RunV("go", "tool", "cover", "-html=./tmp/coverage.out", "-o", "./tmp/coverage.html")
}

// Lint executes all the linters with golangci-lint.
//nolint:deadcode
func Lint() error {
	return sh.RunV("bash", "scripts/lint.sh")
}

// Format reformats code automatically.
//nolint:deadcode
func Format() error {
	err := sh.RunV("gofmt", "-w", ".")
	if err != nil {
		return err
	}
	return sh.RunV("goimports", "-w", "-local=storj", ".")
}

// GenBuild re-generates `./build` helper binary.
//nolint:deadcode
func GenBuild() error {
	envs := map[string]string{
		"CGO_ENABLED": "0",
		"GOOS":        "linux",
		"GOARCH":      "amd64",
	}
	return sh.RunWithV(envs, "mage", "-compile", "build")
}

// DockerBuild builds storjscan image.
//nolint:deadcode
func DockerBuild() error {
	tag, err := getNextDockerTag("storjscan.last")
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "build", "-t", "img.dev.storj.io/storjup/storjscan:"+tag, ".")
	if err != nil {
		return err
	}
	return nil
}

// DockerPublish pushes storjscan image.
//nolint:deadcode
func DockerPublish() error {
	return dockerPushWithNextTag("storjscan")
}

// Integration executes integration tests.
//nolint:deadcode
func Integration() error {
	return sh.RunV("bash", "test/test.sh")
}

// ListImages prints all the existing storjscan images in the repo.
//nolint:deadcode
func ListImages() error {
	versions, err := listContainerVersions("storjscan")
	if err != nil {
		return err
	}
	for _, v := range versions {
		fmt.Printf("storjscan:%s\n", v)
	}
	return nil
}

func dockerPushWithNextTag(image string) error {
	tagFile := fmt.Sprintf("%s.last", image)
	tag, err := getNextDockerTag(tagFile)
	if err != nil {
		return err
	}
	err = sh.RunV("docker", "push", fmt.Sprintf("img.dev.storj.io/storjup/%s:%s", image, tag))
	if err != nil {
		return err
	}
	return writeDockerTag(tagFile, tag)
}

func dockerPush(image string, tag string) error {
	err := sh.RunV("docker", "push", fmt.Sprintf("img.dev.storj.io/storjup/%s:%s", image, tag))
	if err != nil {
		return err
	}
	return err
}

// getNextDockerTag generates docker tag with the pattern yyyymmdd-n.
// last used tag is saved to the file and supposed to be committed.
func getNextDockerTag(tagFile string) (string, error) {
	datePattern := time.Now().Format("20060102")

	if _, err := os.Stat(tagFile); os.IsNotExist(err) {
		return datePattern + "-1", nil
	}

	content, err := ioutil.ReadFile(tagFile)
	if err != nil {
		return "", err
	}
	parts := strings.Split(string(content), "-")
	if parts[0] == datePattern {
		i, err := strconv.Atoi(parts[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s-%d", datePattern, i+1), err

	}
	return datePattern + "-1", nil
}

func doOnMissing(containerName string, repoName string, action func(string, string, string) error) error {
	containerVersions := make(map[string]bool)
	versions, err := listContainerVersions(containerName)
	if err != nil {
		return err
	}
	for _, v := range versions {
		containerVersions[v] = true
	}

	releases, err := listReleaseVersions(repoName)
	if err != nil {
		return err
	}
	for _, v := range releases {
		if _, found := containerVersions[v]; !found {
			err = action(containerName, repoName, v)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// writeDockerTag persist the last used docker tag to a file.
func writeDockerTag(tagFile string, tag string) error {
	return ioutil.WriteFile(tagFile, []byte(tag), 0o644)
}

// ListVersions prints out the available container / release versions.
//nolint:deadcode
func ListVersions() error {
	fmt.Println("container: storjscan")
	versions, err := listContainerVersions("storjscan")
	if err != nil {
		return err
	}
	for _, v := range versions {
		fmt.Println("   " + v)
	}
	return nil
}

func listReleaseVersions(name string) ([]string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/storjscan/%s/releases?per_page=10", name)
	rawVersions, err := callGithubAPIV3(context.Background(), "GET", url, nil)
	if err != nil {
		return nil, err
	}

	var releases []release
	err = json.Unmarshal(rawVersions, &releases)
	if err != nil {
		return nil, err
	}

	var res []string
	for _, v := range releases {
		name := v.TagName
		if strings.Contains(name, "rc") {
			continue
		}
		if name[0] == 'v' {
			name = name[1:]
		}
		res = append(res, name)
	}
	sort.Strings(res)
	return res, nil
}

// listContainerVersions lists the available tags for one specific container
func listContainerVersions(name string) ([]string, error) {
	ctx := context.Background()
	url := fmt.Sprintf("https://img.dev.storj.io/auth?service=img.dev.storj.io&scope=repository:%s:pull", name)
	tokenResponse, err := httpCall(ctx, "GET", url, nil)
	if err != nil {
		return nil, errs.Wrap(err)
	}

	k := struct {
		Token string `json:"token"`
	}{}
	err = json.Unmarshal(tokenResponse, &k)
	if err != nil {
		return nil, errs.Wrap(err)
	}

	url = fmt.Sprintf("https://img.dev.storj.io/v2/storjup/%s/tags/list", name)
	tagResponse, err := httpCall(ctx, "GET", url, nil, func(request *http.Request) {
		request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", k.Token))
	})
	if err != nil {
		return nil, errs.Wrap(err)
	}

	var versions version
	err = json.Unmarshal(tagResponse, &versions)
	if err != nil {
		return nil, err
	}

	var res []string
	for _, version := range versions.Tags {
		if version == "latest" {
			continue
		}
		res = append(res, version)
	}
	return res, nil
}

// callGithubAPIV3 is a wrapper around the HTTP method call.
func callGithubAPIV3(ctx context.Context, method string, url string, body io.Reader) ([]byte, error) {

	token, err := getToken()
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return httpCall(ctx, method, url, body, func(req *http.Request) {
		req.Header.Add("Authorization", "token "+token)
		req.Header.Add("Accept", "application/vnd.github.v3+json")
	})
}

type httpRequestOpt func(*http.Request)

func httpCall(ctx context.Context, method string, url string, body io.Reader, opts ...httpRequestOpt) ([]byte, error) {
	client := &http.Client{}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	for _, o := range opts {
		o(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errs.Wrap(err)
	}

	if resp.StatusCode > 299 {
		return nil, errs.Combine(errs.New("%s url is failed (%s): %s", method, resp.Status, url), resp.Body.Close())
	}
	responseBody, err := ioutil.ReadAll(resp.Body)
	return responseBody, errs.Combine(err, resp.Body.Close())
}

// getToken retrieves the GITHUB_TOKEN for API usage.
func getToken() (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token != "" {
		return token, nil
	}
	return "", fmt.Errorf("GITHUB_TOKEN environment variable must set")
}

// release is a Github API response object.
type release struct {
	URL             string    `json:"url"`
	AssetsURL       string    `json:"assets_url"`
	UploadURL       string    `json:"upload_url"`
	HTMLURL         string    `json:"html_url"`
	ID              int       `json:"id"`
	NodeID          string    `json:"node_id"`
	TagName         string    `json:"tag_name"`
	TargetCommitish string    `json:"target_commitish"`
	Name            string    `json:"name"`
	Draft           bool      `json:"draft"`
	Prerelease      bool      `json:"prerelease"`
	CreatedAt       time.Time `json:"created_at"`
	PublishedAt     time.Time `json:"published_at"`
	TarballURL      string    `json:"tarball_url"`
	ZipballURL      string    `json:"zipball_url"`
	Body            string    `json:"body"`
	MentionsCount   int       `json:"mentions_count,omitempty"`
}

// version is a Docker v2 REST API response object.
type version struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}