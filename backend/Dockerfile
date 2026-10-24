# syntax=docker/dockerfile:1.7
# Çok aşamalı derleme: Go derleyici yalnızca build aşamasında; çalışma imajları
# distroless/static (kabuk yok, root olmayan kullanıcı 65532, ~25 MB).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/verify ./cmd/verify && \
    mkdir -p /out/data/uploads

FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/api /app/api
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/api"]

FROM gcr.io/distroless/static-debian12:nonroot AS simulator
COPY --from=build /out/simulator /app/simulator
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
ENTRYPOINT ["/app/simulator"]
CMD ["run"]

FROM gcr.io/distroless/static-debian12:nonroot AS verify
COPY --from=build /out/verify /app/verify
USER nonroot:nonroot
ENTRYPOINT ["/app/verify"]
