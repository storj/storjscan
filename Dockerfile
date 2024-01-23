# syntax=docker/dockerfile:1

# parent image
FROM golang:1.20-alpine AS builder

# Build Delve
RUN go install github.com/go-delve/delve/cmd/dlv@latest

#install build dependencies
RUN apk add build-base

# workspace directory
WORKDIR /app

# copy `go.mod` and `go.sum`
ADD go.mod go.sum ./

# install dependencies
RUN go mod download

# copy source code
COPY . .

# build executable
RUN go build -gcflags="all=-N -l" -o build/ ./cmd/storjscan

##################################

# parent image
FROM alpine:3.12.2

# copy binary file from the `builder` stage
COPY --from=builder /app/build/storjscan /var/lib/storj/go/bin/
COPY --from=builder /go/bin/dlv /var/lib/storj/go/bin/
ADD entrypoint.sh /var/lib/storj/entrypoint.sh

ENV PATH="/var/lib/storj/go/bin/:${PATH}"

# exec
ENTRYPOINT ["/var/lib/storj/entrypoint.sh"]