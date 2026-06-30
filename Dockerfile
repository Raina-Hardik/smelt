# Use a blank slate
FROM scratch

# Receives the architecture argument from GitHub Actions
ARG TARGETARCH

# Copy the pre-compiled linux binary matching the architecture
COPY smelt-linux-${TARGETARCH} /usr/local/bin/smelt

ENTRYPOINT ["/usr/local/bin/smelt"]
