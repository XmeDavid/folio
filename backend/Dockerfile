# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags "-s -w" \
      -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags "-s -w" \
      -o /out/folio-river-migrate ./cmd/folio-river-migrate

FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
COPY --from=build /out/folio-river-migrate /app/folio-river-migrate
COPY --from=build /src/db/migrations /app/db/migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
