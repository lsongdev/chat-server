FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/chat-server .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 chat
USER chat
COPY --from=build /out/chat-server /usr/local/bin/chat-server
EXPOSE 8080
ENTRYPOINT ["chat-server"]
