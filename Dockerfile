# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.22 as builder

# download kubebuilder and extract it to tmp
ARG BUILDOS BUILDARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.* .

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Build dependencies
ARG TARGETOS TARGETARCH
ENV GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build std

# Copy rest of project
COPY . /workspace

# Run tests
RUN make test

# Build
RUN CGO_ENABLED=0 make all

FROM gcr.io/distroless/static-debian11
WORKDIR /
COPY --from=builder /workspace/bin/setup /setup
COPY --from=builder /workspace/bin/promote /promote
COPY --from=builder /workspace/bin/cleanup /cleanup
COPY --from=builder /workspace/bin/rollback /rollback
