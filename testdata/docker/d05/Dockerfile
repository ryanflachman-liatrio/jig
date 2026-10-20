# Explicit preparation only; workflow execution never builds or pulls images.
FROM golang:1.25.12-bookworm@sha256:6359592445455f2dbe2412bed411336035bc019a50017720d77454ffdd6d0f82
WORKDIR /opt/jig-dependencies
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=local go mod download && chmod -R a-w /go/pkg/mod
ENV GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOCACHE=/scratch/go-build HOME=/home/jig
WORKDIR /workspace
USER 1000:1000
ENTRYPOINT []
CMD ["sh"]
