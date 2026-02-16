FROM golang:1.25 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tinymon-operator .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /app/tinymon-operator .
USER 65532:65532
ENTRYPOINT ["/tinymon-operator"]
