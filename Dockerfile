# Build the vals-operator binary
FROM golang:1.16 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod /workspace/go.mod
COPY go.sum /workspace/go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY . /workspace

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o vals-operator main.go

# Use distroless as minimal base image to package the vals-operator binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/vals-operator /vals-operator
USER 65532:65532

ENTRYPOINT ["/vals-operator"]
