FROM ghcr.io/blinklabs-io/go:1.24.4-1 AS build

WORKDIR /code
COPY . .
RUN make build

FROM alpine:latest AS setup
# Create the directory structure with proper permissions for the non-root user
RUN mkdir -p /data/assets/logs && \
    chown -R 65532:65532 /data && \
    chmod -R 755 /data

FROM cgr.dev/chainguard/glibc-dynamic AS node

# Copy the pre-created directory structure from setup stage
COPY --from=setup /data /data

COPY --from=build /code/node /bin/
COPY --chown=65532:65532 --from=build /code/docs /data/docs

ENV PORT=8080
EXPOSE 8080

VOLUME /data
WORKDIR /data

# Switch back to non-root user
USER 65532

ENTRYPOINT ["node", "-config", "/etc/config/config.yaml"]