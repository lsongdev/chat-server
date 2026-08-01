# --- Frontend build ---
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# --- Go build ---
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/chat-server .

# --- Runtime ---
FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 chat
USER chat
COPY --from=build /out/chat-server /usr/local/bin/chat-server
EXPOSE 8080
ENTRYPOINT ["chat-server"]
