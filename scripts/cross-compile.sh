#!/usr/bin/env bash

cd "$(dirname "${BASH_SOURCE[0]}")/.."

docker run -v `pwd`:/opt/storjscan -w /opt/storjscan ghcr.io/elek/storj-build:20220412-1 go build -o cmd/storjscan/storjscan storj.io/storjscan/cmd/storjscan
