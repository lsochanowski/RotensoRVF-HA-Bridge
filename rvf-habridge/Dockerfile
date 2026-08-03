FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
    -o /rvf-habridge ./cmd/rvf-habridge

# No Home Assistant base image on purpose: the s6-overlay init in those
# images strips the environment for the legacy CMD process, so the
# add-on never sees SUPERVISOR_TOKEN. A static binary on plain Alpine
# with the Supervisor-provided docker init works and is smaller.
FROM alpine:3.20
COPY --from=build /rvf-habridge /usr/bin/rvf-habridge
ENTRYPOINT ["/usr/bin/rvf-habridge", "bridge"]
