FROM --platform=$BUILDPLATFORM golang:1.27@sha256:007a316059246276cb214df93950be77837545dc78007a73e0291a4963d5d3bf AS build
ARG TARGETOS=linux TARGETARCH
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /manager ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]
