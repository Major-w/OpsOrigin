FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum go.work ./
COPY . .
RUN go build -o /opsorigin-core ./cmd/core/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /opsorigin-core /usr/local/bin/
EXPOSE 8080 5588
CMD ["opsorigin-core"]
