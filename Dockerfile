# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
ARG TAILWIND_VERSION=v4.3.3
ARG TARGETARCH
WORKDIR /src
RUN apk add --no-cache curl make
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN arch=$([ "$TARGETARCH" = "arm64" ] && echo arm64 || echo x64) \
    && curl -sSL -o /usr/local/bin/tailwindcss "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${arch}-musl" \
    && chmod +x /usr/local/bin/tailwindcss \
    && tailwindcss -i web/static/css/input.css -o web/static/css/app.css --minify \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/partners ./cmd/partners \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/partners /partners
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV PARTNERS_DATA_DIR=/data \
    PARTNERS_LISTEN=:8080
VOLUME /data
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/partners"]
CMD ["serve"]
