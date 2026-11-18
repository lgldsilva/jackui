# Stage 1: Build frontend
FROM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS frontend


WORKDIR /app/web

COPY web/package.json web/package-lock.json* ./
RUN npm ci --ignore-scripts

COPY web/ ./


ARG BUILD_TIMESTAMP
RUN echo "Build at $BUILD_TIMESTAMP" && npm run build
# Output goes to /app/ui/dist (configured in vite.config.ts)

# Stage 2: Build backend
FROM golang:1.26.10-alpine3.24@sha256:87c65a688975c0b1d4585d204874c45ca78354670e3a5f11368c5a492303d696 AS backend
ENV GOTOOLCHAIN=auto

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY ui/ ./ui/
COPY --from=frontend /app/ui/dist ./ui/dist

# Build metadata injected into internal/version (served by GET /status). The
# build runs from a tree WITHOUT .git, so the commit arrives as a build-arg
# (CI passes $GIT_COMMIT; the Makefile passes `git rev-parse HEAD`).
ARG GIT_COMMIT=unknown
ARG BUILD_TIMESTAMP=unknown
ARG APP_VERSION=dev
RUN go build -trimpath -ldflags "-s -w \
      -X github.com/lgldsilva/jackui/internal/version.Commit=${GIT_COMMIT} \
      -X github.com/lgldsilva/jackui/internal/version.BuildTime=${BUILD_TIMESTAMP} \
      -X github.com/lgldsilva/jackui/internal/version.Version=${APP_VERSION}" \
      -o jackui ./cmd/server

# Stage 3: Final image
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

RUN apk add --no-cache ca-certificates tzdata ffmpeg && \
    addgroup -g 1000 jackui && \
    adduser -u 1000 -G jackui -h /app -s /bin/sh -D jackui

WORKDIR /app
COPY --from=backend /app/jackui .

# Transfer ownership of execution folder to the unprivileged user
RUN chown -R jackui:jackui /app

USER jackui

EXPOSE 8989

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD ["/app/jackui", "healthcheck"]

ENTRYPOINT ["./jackui"]
