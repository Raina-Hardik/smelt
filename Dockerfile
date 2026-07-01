# smelt is a process ORCHESTRATOR, not a self-contained binary: it shells out to
# ffmpeg/ffprobe (and, for surgical DV/HDR work, optional tools like dovi_tool).
# A `scratch` base has none of those, so the image could run `smelt version` but
# every real transcode died with "ffmpeg not found on PATH". Base on Alpine and
# install ffmpeg (ffprobe ships in the same package) so the image actually works.
#
# Notes:
#   - The CGO-disabled static binary runs fine against musl.
#   - Alpine's ffmpeg lacks non-free encoders (e.g. libfdk_aac); hardware accel
#     needs host device passthrough. Acceptable for a general-purpose image.
FROM alpine:3.20

RUN apk add --no-cache ffmpeg

# Receives the architecture argument from GitHub Actions (buildx resolves the
# matching Alpine layers per-arch automatically).
ARG TARGETARCH

# Copy the pre-compiled linux binary matching the architecture
COPY smelt-linux-${TARGETARCH} /usr/local/bin/smelt

ENTRYPOINT ["/usr/local/bin/smelt"]
