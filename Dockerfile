FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/proxy ./cmd/proxy

FROM alpine:3.21

RUN apk add --no-cache \
    poppler-utils \
    tesseract-ocr \
    tesseract-ocr-data-eng \
    curl

WORKDIR /app
COPY --from=builder /app/bin/proxy .

RUN mkdir -p /data

EXPOSE 4000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD curl -sf http://localhost:4000/healthz || exit 1

CMD ["./proxy"]
