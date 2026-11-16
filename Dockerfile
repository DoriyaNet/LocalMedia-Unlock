# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/localmedia-unlock .

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chown=65532:65532 /out/localmedia-unlock /usr/local/bin/localmedia-unlock

USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
EXPOSE 9090 7890

ENTRYPOINT ["/usr/local/bin/localmedia-unlock"]
CMD ["serve", "--config", "/data/config.json", "--listen", "0.0.0.0:9090"]

