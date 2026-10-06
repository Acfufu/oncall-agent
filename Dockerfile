FROM node:24-alpine AS frontend
WORKDIR /src/web/app
COPY web/app/package*.json ./
RUN npm ci
COPY web/app/ ./
RUN npm run build

FROM golang:1.25-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN --mount=type=cache,target=/root/.cache/go-build GOMAXPROCS=2 CGO_ENABLED=1 go build -p 2 -trimpath -o /out/oncall-agent ./cmd/server && GOMAXPROCS=2 CGO_ENABLED=1 go build -p 2 -trimpath -o /out/workspacectl ./cmd/workspacectl

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home oncall
WORKDIR /app
COPY --from=backend /out/ /usr/local/bin/
COPY config/config_template.json config/metric_templates.json /app/config/
COPY aiops-docs-demo/ /app/aiops-docs-demo/
COPY web/console.html web/index.html /app/web/
COPY --from=frontend /src/web/dist/ /app/web/dist/
RUN mkdir /app/data && chown oncall:oncall /app/data
USER oncall
ENV ONCALL_CONFIG=/app/config/config_template.json ONCALL_SQLITE_PATH=/app/data/facts.sqlite
EXPOSE 8819
CMD ["oncall-agent"]
