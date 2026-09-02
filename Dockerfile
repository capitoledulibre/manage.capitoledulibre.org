FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /benevoles .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 1000 app
COPY --from=build /benevoles /usr/local/bin/benevoles
VOLUME /data
WORKDIR /data
USER app
ENV ADDR=:8080 DB_PATH=/data/benevoles.db UPLOAD_DIR=/data/uploads
EXPOSE 8080
ENTRYPOINT ["benevoles"]
CMD ["serve"]
