FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /mrinspect ./cmd/mrinspect
RUN mkdir -p /app-rag && /mrinspect index --out /app-rag/mrinspect-rag.sqlite

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY --from=builder /mrinspect /usr/local/bin/mrinspect
RUN mkdir -p /app/.rag
COPY --from=builder /app-rag/mrinspect-rag.sqlite /app/.rag/mrinspect-rag.sqlite
COPY projects/ ./projects/
ENTRYPOINT ["mrinspect"]
