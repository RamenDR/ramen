# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# Build the manager binary
FROM docker.io/library/golang:1.19.1 as builder

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
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o manager main.go

FROM registry.access.redhat.com/ubi8/ubi
WORKDIR /
COPY --from=builder /workspace/manager .

# copy licenses to license folder
RUN mkdir -p licenses
COPY LICENSES/Apache-2.0.txt licenses/Apache-2.0.txt

USER 65532:65532

ENTRYPOINT ["/manager"]
