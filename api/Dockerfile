FROM golang:1.25-alpine AS build
WORKDIR /app
COPY shared ./shared
COPY api ./api
WORKDIR /app/api
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/api .

FROM alpine:3.20
RUN apk add --no-cache docker-cli ca-certificates
WORKDIR /app
COPY --from=build /bin/api /bin/api
COPY dashboard ./dashboard
EXPOSE 8080
CMD ["/bin/api"]
