FROM golang:1.25.1-bookworm AS build

WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lyrics-api ./cmd

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg yt-dlp \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 lyrics \
    && useradd --system --uid 10001 --gid lyrics --home-dir /app lyrics \
    && mkdir -p /app/tools/ffmpeg/bin /app/tools/yt-dlp /data/renders /data/logs \
    && ln -s /usr/bin/ffmpeg /app/tools/ffmpeg/bin/ffmpeg \
    && ln -s /usr/bin/ffprobe /app/tools/ffmpeg/bin/ffprobe \
    && ln -s /usr/bin/yt-dlp /app/tools/yt-dlp/yt-dlp

WORKDIR /app
COPY --from=build --chown=lyrics:lyrics /out/lyrics-api /usr/local/bin/lyrics-api
COPY --chown=lyrics:lyrics backend/assets/ /app/assets/
RUN chown -R lyrics:lyrics /data /app/tools /app/assets

ENV PORT=8080 \
    LYRICS_DB_PATH=/data/lyrics.db \
    LYRICS_FFMPEG_DIR=/app/tools \
    LYRICS_ASSETS_DIR=/app/assets \
    LYRICS_RENDER_DIR=/data/renders

EXPOSE 8080
USER lyrics
ENTRYPOINT ["/usr/local/bin/lyrics-api"]