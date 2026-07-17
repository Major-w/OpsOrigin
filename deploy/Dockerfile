FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY pkg/ ./pkg/

RUN go mod download && CGO_ENABLED=0 go build -o /core ./cmd/core

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /core /usr/local/bin/core
EXPOSE 8080
ENTRYPOINT ["core"]
