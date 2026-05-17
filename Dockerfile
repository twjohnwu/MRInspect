FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /mrinspect ./cmd/mrinspect

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY --from=builder /mrinspect /usr/local/bin/mrinspect
COPY projects/ ./projects/
ENTRYPOINT ["mrinspect"]
