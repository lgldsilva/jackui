# Stage 1: Build frontend
FROM node:20-alpine AS frontend

# ImageMagick renders SVG → PNG for PWA icons (iOS requires PNG for apple-touch-icon)
RUN apk add --no-cache imagemagick imagemagick-svg

WORKDIR /app/web

COPY web/package.json web/package-lock.json* ./
RUN npm ci

COPY web/ ./

# Generate PNG icons from the SVG source (only if SVG exists)
RUN if [ -f public/favicon.svg ]; then \
      magick -background none -density 1024 public/favicon.svg -resize 192x192 public/icon-192.png && \
      magick -background none -density 1024 public/favicon.svg -resize 512x512 public/icon-512.png; \
    fi

ARG BUILD_TIMESTAMP
RUN echo "Build at $BUILD_TIMESTAMP" && npm run build
# Output goes to /app/ui/dist (configured in vite.config.ts)

# Stage 2: Build backend
FROM golang:1.24-alpine AS backend
ENV GOTOOLCHAIN=auto

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY ui/ ./ui/
COPY --from=frontend /app/ui/dist ./ui/dist

RUN go build -o jackui ./cmd/server

# Stage 3: Final image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata ffmpeg

# Create a non-privileged group and user (UID/GID 1000 matches standard first host user)
RUN addgroup -g 1000 jackui && \
    adduser -u 1000 -G jackui -h /app -s /bin/sh -D jackui

WORKDIR /app
COPY --from=backend /app/jackui .

# Transfer ownership of execution folder to the unprivileged user
RUN chown -R jackui:jackui /app

USER jackui

EXPOSE 8989

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD wget -qO- http://localhost:8989/healthz || exit 1

ENTRYPOINT ["./jackui"]
