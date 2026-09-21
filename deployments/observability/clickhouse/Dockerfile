FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY histogramQuantile.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/histogramQuantile histogramQuantile.go

FROM clickhouse/clickhouse-server:24.1.2-alpine

COPY --from=builder /out/histogramQuantile /usr/local/bin/histogramQuantile
COPY custom-function.xml /etc/clickhouse-server/custom-function.xml
COPY user-functions.xml /etc/clickhouse-server/config.d/user-functions.xml
COPY docker-entrypoint-wrapper.sh /docker-entrypoint-wrapper.sh
RUN chmod +x /docker-entrypoint-wrapper.sh /usr/local/bin/histogramQuantile

ENTRYPOINT ["/docker-entrypoint-wrapper.sh"]
