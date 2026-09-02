FROM golang:1.23-alpine AS build

WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/hushi-website \
    ./cmd/hushi-website

FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && addgroup -S hushi \
    && adduser -S -G hushi hushi \
    && mkdir -p /data/releases \
    && chown -R hushi:hushi /data

WORKDIR /app
COPY --from=build /out/hushi-website /app/hushi-website

ENV HUSHI_WEBSITE_ADDR=:8080 \
    HUSHI_WEBSITE_RELEASE_DIR=/data/releases

EXPOSE 8080
USER hushi
ENTRYPOINT ["/app/hushi-website"]
