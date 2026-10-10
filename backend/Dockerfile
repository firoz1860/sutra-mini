FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -o /out/api ./cmd/api \
 && go build -o /out/worker ./cmd/worker \
 && go build -o /out/mock-accounting ./cmd/mock-accounting \
 && go build -o /out/migrate ./cmd/migrate

FROM alpine:3.20
RUN apk add --no-cache ca-certificates bash && adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/ /app/
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh && chown -R app /app
USER app
ENV MOCK_PORT=8090 \
    ACCOUNTING_URL=http://localhost:8090 \
    MOCK_MODE=success
CMD ["/app/docker-entrypoint.sh"]
