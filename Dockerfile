# Stage 1: Build web frontend
FROM node:20-alpine AS web-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm install --legacy-peer-deps
COPY web/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.25-alpine AS go-builder
ARG VERSION=dev
RUN apk add --no-cache tzdata
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/web/build internal/web/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.version=${VERSION}" -o /synapbus ./cmd/synapbus/

# Stage 3: Runtime
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=go-builder /synapbus /synapbus
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/synapbus"]
CMD ["serve", "--host", "0.0.0.0", "--port", "8080", "--data", "/data"]
