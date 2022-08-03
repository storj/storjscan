# syntax=docker/dockerfile:1

# parent image
FROM golang:1.18-alpine AS builder

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
RUN go build -o build/ ./cmd/storjscan

##################################

# parent image
FROM alpine:3.12.2

# workspace directory
WORKDIR /app

# copy binary file from the `builder` stage
COPY --from=builder /app/build/storjscan ./

# exec
CMD ["/app/storjscan", "run"]