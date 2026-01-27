FROM alpine

ARG TARGETOS
ARG TARGETARCH
EXPOSE 5999

WORKDIR /app

VOLUME /root/.local/share/storj/storjscan
ENV STORJ_CONFIG_DIR=/root/.local/share/storj/storjscan

COPY cmd/storjscan/entrypoint /app/entrypoint
COPY --from=binaries /${TARGETOS}_${TARGETARCH}/storjscan /app/storjscan

ENTRYPOINT ["/app/entrypoint"]
