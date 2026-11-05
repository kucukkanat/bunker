# Bunker container image: a single static Go binary on distroless/static.
#
#   docker build -t bunker .
#   docker run -p 8080:8080 -v bunker-data:/data bunker
#
# The first start prints a setup URL and a one-time setup code in the logs.
#
# The server runs as the distroless "nonroot" user (uid 65532). Some hosts
# (Render, Railway) mount persistent disks owned by root and give no
# way to chown them; their configs build with BUNKER_RUN_UID=0 instead.

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=auto
COPY go.mod go.sum ./
# No BuildKit cache mounts: some PaaS builders (Railway, Heroku) reject them.
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/kucukkanat/bunker/internal/core.Version=${VERSION}" \
      -o /out/bunker ./cmd/bunker
# An empty /data owned by the nonroot user, so named volumes inherit the owner.
RUN mkdir -p /out/data

# distroless/static: CA certificates, tzdata, /etc/passwd with nonroot (65532).
FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG BUNKER_RUN_UID=65532
LABEL org.opencontainers.image.title="Bunker" \
      org.opencontainers.image.description="Internal apps anyone can build. IT can trust." \
      org.opencontainers.image.source="https://github.com/kucukkanat/bunker" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/bunker /usr/local/bin/bunker
COPY --from=build --chown=${BUNKER_RUN_UID}:${BUNKER_RUN_UID} /out/data /data
USER ${BUNKER_RUN_UID}:${BUNKER_RUN_UID}
ENV BUNKER_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/bunker", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/bunker"]
CMD ["serve"]
