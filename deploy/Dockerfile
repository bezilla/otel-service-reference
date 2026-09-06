# Multi-stage: the runtime image carries the binary and nothing else.
# Both stages are pinned by digest, not by tag. golang:1.27-alpine is a moving
# target by design and distroless :nonroot is rebuilt continuously, so a tag
# alone means this Dockerfile builds a different image next week with no diff
# here. Renovate moves the digest and the tag comment together.
FROM golang:1.27-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS build
WORKDIR /src

# Copy the module files first so dependency download is cached separately from
# source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/service ./cmd/service

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/service /service
USER nonroot:nonroot
EXPOSE 8080 8081
ENTRYPOINT ["/service"]
