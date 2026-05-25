# Stage 1: Build frontend
FROM node:20-alpine AS frontend

WORKDIR /app/web

COPY web/package.json web/package-lock.json* ./
RUN npm ci

COPY web/ ./
RUN npm run build
# Output goes to /app/ui/dist (configured in vite.config.ts)

# Stage 2: Build backend
FROM golang:1.22-alpine AS backend

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

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=backend /app/jackui .

EXPOSE 8989

ENTRYPOINT ["./jackui"]
