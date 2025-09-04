FROM ghcr.io/blinklabs-io/go:1.24.4-1 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS node
COPY --from=build /code/node /bin/
COPY --from=build /code/docs /data/docs

VOLUME /data
WORKDIR /data
ENTRYPOINT ["node"]
