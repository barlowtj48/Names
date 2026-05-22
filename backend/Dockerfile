# Stage 1: build Go binary
FROM golang:1.25.0 AS go-builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY backend/ ./backend/
COPY shared/ ./shared/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -o /app/backend/main ./backend

# Stage 2: minimal Alpine runtime
FROM alpine:3.22 AS final

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /opt/app

COPY --from=go-builder /app/backend/main /opt/app/backend
COPY backend/templates /opt/app/templates
COPY backend/static    /opt/app/static

RUN chmod +x /opt/app/backend

CMD ["./backend"]
