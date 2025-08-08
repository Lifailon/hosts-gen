# Build image
FROM golang:1.23-alpine3.20 AS build
WORKDIR /hosts-gen
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
ARG TARGETARCH TARGETOS
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /hosts-gen

# Final image
FROM alpine:3.20
COPY --from=build /hosts-gen /usr/local/bin/
# for post commands
RUN apk add curl docker-cli

ENTRYPOINT ["hosts-gen"]