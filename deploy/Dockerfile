# Multi-stage: the runtime image carries the binary and nothing else.
FROM golang:1.27-alpine AS build
WORKDIR /src

# Copy the module files first so dependency download is cached separately from
# source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/service

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/service /service
USER nonroot:nonroot
EXPOSE 8080 8081
ENTRYPOINT ["/service"]
