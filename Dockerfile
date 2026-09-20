# The built browser bundles are committed to web/dist, so the image can be
# produced from Go alone. The Node stage exists to rebuild them from source
# when you want to be certain the binary matches web/src rather than trusting
# whatever was last committed; it is cheap and keeps the two honest.

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
# The build needs only esbuild and typescript, and no postinstall scripts.
RUN npm ci --ignore-scripts 2>/dev/null || npm install --ignore-scripts
RUN npm rebuild esbuild
COPY web/ ./
RUN node build.mjs


FROM golang:1.24-alpine AS build
WORKDIR /src

# Dependencies first, so a source-only change does not redownload anything.
# This project has no external Go modules, but the layer costs nothing and
# keeps the Dockerfile correct if that ever changes.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
COPY --from=web /src/web/dist ./web/dist

ARG VERSION=dev
# CGO is off so the result is a static binary that runs on a scratch image.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/captchad ./cmd/captchad
RUN go vet ./... && go test ./...


FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /
COPY --from=build /out/captchad /captchad
# A configuration file is optional: PCAPTCHA_SITE_KEY and PCAPTCHA_SITE_SECRET
# are enough to start. Mount one at /etc/captcha/captcha.yaml to use a file.
ENV PCAPTCHA_CONFIG=/etc/captcha/captcha.yaml
EXPOSE 8080
USER nonroot:nonroot

# Every asset is embedded in the binary, so there is nothing else to ship.
ENTRYPOINT ["/captchad"]
