ARG DOCKER_ARCH
FROM ${DOCKER_ARCH:-amd64}/alpine

ARG TAG
ARG GOARCH

ENV GOARCH ${GOARCH}

EXPOSE 5999
WORKDIR /app

VOLUME /root/.local/share/storj/storjscan

COPY release/${TAG}/storjscan_linux_${GOARCH:-amd64} /app/storjscan
COPY cmd/storjscan/entrypoint /entrypoint

ENTRYPOINT ["/entrypoint"]

ENV STORJ_CONFIG_DIR=/root/.local/share/storj/storjscan
