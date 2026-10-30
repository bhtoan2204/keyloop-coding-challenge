# Inventory service image: one static binary for every subcommand
# (api, migrate, rebuild-projections, healthcheck).

FROM docker.io/library/golang:1.26.10-alpine3.24@sha256:d1f3216ef30257c850cc136ba42a359ccd9d3e75f19b2c276e520c0b4ef327ce AS build
WORKDIR /src
# Dependencies first, so source edits do not invalidate the module cache layer.
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY package ./package
# No cgo: the service needs no C library (SQLite is used by tests only), so the binary
# runs on a distroless static base. Time zones are embedded (time/tzdata in cmd).
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/inventory ./cmd

# Distroless static, nonroot (uid 65532): no shell, no package manager.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:52dcfbabb7457ea47c82f6e13af8c8a4a1d9f7b0145142b3ecab20f2b888411d
COPY --from=build /out/inventory /inventory
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/inventory"]
CMD ["api"]
