# syntax=docker/dockerfile:1

# Base images are pinned by digest: a rebuild gets exactly the same ones.
# Refresh the digests on purpose, for a Go or security update.
FROM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build
ARG TAILWIND_VERSION=v4.3.5
ARG TARGETARCH
WORKDIR /src
# The musl build of the Tailwind CLI links against the C++ runtime.
RUN apk add --no-cache curl make libstdc++ libgcc
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The Tailwind CLI runs in the build: it must be the binary of the release,
# checked against tailwindcss.sha256.
RUN arch=$([ "$TARGETARCH" = "arm64" ] && echo arm64 || echo x64) \
    && name="tailwindcss-linux-${arch}-musl" \
    && curl -sSLf -o /usr/local/bin/tailwindcss "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/${name}" \
    && echo "$(awk -v n="$name" '$2 == n { print $1 }' tailwindcss.sha256)  /usr/local/bin/tailwindcss" | sha256sum -c - \
    && chmod +x /usr/local/bin/tailwindcss \
    && tailwindcss -i web/static/css/input.css -o web/static/css/app.css --minify \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/manage ./cmd/manage \
    && mkdir -p -m 0700 /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/manage /manage
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV MANAGE_DATA_DIR=/data \
    MANAGE_LISTEN=:8080
VOLUME /data
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/manage"]
CMD ["serve"]
