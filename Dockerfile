# Multi-stage build for the cofiswarm-dispatch sidecar.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd/cofiswarm-dispatch

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/app /usr/local/bin/cofiswarm-dispatch
USER app
EXPOSE 8010
ENTRYPOINT ["/usr/local/bin/cofiswarm-dispatch"]
