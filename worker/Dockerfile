FROM golang:1.22-alpine AS build
WORKDIR /app
COPY shared ./shared
COPY worker ./worker
WORKDIR /app/worker
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/worker .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /bin/worker /bin/worker
CMD ["/bin/worker"]
