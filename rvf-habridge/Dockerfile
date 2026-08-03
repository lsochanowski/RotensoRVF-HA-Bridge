ARG BUILD_FROM=ghcr.io/home-assistant/amd64-base:latest

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
    -o /rvf-habridge ./cmd/rvf-habridge

FROM $BUILD_FROM
COPY --from=build /rvf-habridge /usr/bin/rvf-habridge
CMD ["/usr/bin/rvf-habridge", "bridge"]
