# Build the manager binary
FROM golang:1.16 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager main.go

# https://sdk.operatorframework.io/docs/building-operators/golang/tutorial/#configure-the-operators-image-registry
FROM kgibm/containerdiagsmall:latest
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65534:65534

ENTRYPOINT ["/manager"]
