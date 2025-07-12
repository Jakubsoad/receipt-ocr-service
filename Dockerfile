FROM golang:1.17-alpine as builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ocr-service .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/ocr-service .
COPY service-account.json .

ENV GOOGLE_APPLICATION_CREDENTIALS="/root/service-account.json"
ENV PORT=8081

EXPOSE 8080
CMD ["./ocr-service"]