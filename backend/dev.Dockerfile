FROM golang:1.25.1

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

RUN go install github.com/air-verse/air@v1.63.0
RUN go install github.com/go-delve/delve/cmd/dlv@v1.25.2

EXPOSE 2345 8104

CMD ["air", "-c", "backend/.air.toml"]
