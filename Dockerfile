# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0: the sqlite driver (glebarez/go-sqlite -> modernc.org/sqlite)
# is pure Go, so the binary stays fully static - no libc needed at runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/servicedesk ./cmd/servicedesk
# distroless has no shell/mkdir; pre-create the data dir here so it can be
# COPY --chown'd into the runtime image owned by the nonroot user (65532).
# A named volume mounted over it inherits this ownership on first creation.
RUN mkdir -p /out/data

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/servicedesk /app/servicedesk
COPY --from=build --chown=65532:65532 /out/data /data

USER nonroot:nonroot
EXPOSE 8080
VOLUME ["/data"]
ENV SERVICEDESK_ADDR=:8080 \
    SERVICEDESK_DB_DRIVER=sqlite \
    SERVICEDESK_DB_DSN="file:/data/servicedesk.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"

ENTRYPOINT ["/app/servicedesk"]
