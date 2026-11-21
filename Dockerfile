FROM node:26-alpine AS web
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY vite.config.ts tsconfig.json ./
COPY scripts/check-csp.mjs ./scripts/
COPY src ./src
COPY static ./static
RUN npm run build

FROM golang:1.27.4-alpine AS api
ARG VERSION=dev
WORKDIR /src
COPY backend-go/go.mod backend-go/go.sum ./
RUN go mod download
COPY backend-go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/api ./cmd/api

# Pinned by digest: CI scans this image and then rebuilds it to push, so
# both builds must start from the same base. Renovate bumps the digest.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=api /out/api /usr/local/bin/api
COPY --from=web /src/build /app/web
ENV OHARA_ADDR=:8000 \
    OHARA_WEB_DIR=/app/web
EXPOSE 8000
USER nonroot:nonroot
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=5 \
    CMD ["/usr/local/bin/api", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/api"]
