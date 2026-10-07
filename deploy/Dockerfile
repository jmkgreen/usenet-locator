FROM node:24-bookworm-slim AS frontend-build
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm run build

FROM golang:1.27-bookworm AS backend-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY backend/ ./backend/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/usenet-locator ./backend/cmd/usenet-locator

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend-build /out/usenet-locator /usenet-locator
COPY --from=frontend-build /src/frontend/dist /web
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usenet-locator"]
