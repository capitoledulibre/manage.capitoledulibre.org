# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
ARG TAILWIND_VERSION=v4.3.3
ARG TARGETARCH
WORKDIR /src
# The musl build of the Tailwind CLI links against the C++ runtime.
RUN apk add --no-cache curl make libstdc++ libgcc
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN arch=$([ "$TARGETARCH" = "arm64" ] && echo arm64 || echo x64) \
    && curl -sSL -o /usr/local/bin/tailwindcss "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${arch}-musl" \
    && chmod +x /usr/local/bin/tailwindcss \
    && tailwindcss -i web/static/css/input.css -o web/static/css/app.css --minify \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/manage ./cmd/manage \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/manage /manage
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV MANAGE_DATA_DIR=/data \
    MANAGE_LISTEN=:8080
VOLUME /data
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/manage"]
CMD ["serve"]
