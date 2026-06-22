#!/usr/bin/env bash
# Spawn a clean real-media testing environment for smelt.
#
# Downloads public test media (Blender open movies + Jellyfish bitrate samples)
# into ./smelt-sample-testcase/ at the repo root so contributors can test their
# changes against real files. Downloads are resumable; safe to re-run.
#
# Interactive (gum) when run from a TTY; falls back to fetching everything
# non-interactively otherwise. Installs gum via `go install` if it is missing.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEST="$REPO_ROOT/smelt-sample-testcase"

# Ensure gum (Charmbracelet's shell UX toolkit) is available.
if ! command -v gum >/dev/null 2>&1; then
  echo "gum not found; installing via 'go install github.com/charmbracelet/gum@latest'..."
  go install github.com/charmbracelet/gum@latest
  GOBIN="$(go env GOBIN)"
  [ -z "$GOBIN" ] && GOBIN="$(go env GOPATH)/bin"
  export PATH="$GOBIN:$PATH"
fi

interactive=0
if [ -t 0 ] && command -v gum >/dev/null 2>&1; then
  interactive=1
fi

# Pick which media sets to fetch (default: all).
ALL=$'blender\njellyfish\n4k'
if [ "$interactive" -eq 1 ]; then
  sets="$(gum choose --no-limit --selected="blender,jellyfish,4k" \
    --header "Select media sets to fetch:" blender jellyfish 4k || true)"
  [ -z "$sets" ] && sets="$ALL"
else
  sets="$ALL"
fi

want() { grep -qx "$1" <<<"$sets"; }

if [ "$interactive" -eq 1 ]; then
  gum confirm "Download selected media into $DEST? This can be several GB." \
    || { echo "Aborted."; exit 0; }
fi

mkdir -p "$DEST"
cd "$DEST"

# -f: fail on HTTP errors  -L: follow redirects  -C -: resume  --retry: resilient
fetch() {
  local out="$1" url="$2"
  mkdir -p "$(dirname "$out")"
  if [ "$interactive" -eq 1 ]; then
    gum spin --title "Fetching $out" -- \
      curl -fL -C - --retry 5 --retry-delay 5 --retry-all-errors -o "$out" "$url"
  else
    echo ">> $out"
    curl -fL -C - --retry 5 --retry-delay 5 --retry-all-errors -o "$out" "$url"
  fi
}

# Blender open movies — varied resolutions/containers.
if want blender; then
  fetch blender/bbb_480p.mp4    "https://download.blender.org/peach/bigbuckbunny_movies/BigBuckBunny_320x180.mp4"
  fetch blender/sintel_720p.mkv "https://download.blender.org/demo/movies/Sintel.2010.720p.mkv"
  fetch blender/tos_1080p.mkv   "https://archive.org/download/Tears-of-Steel/tears_of_steel_1080p.mkv"
fi

# Jellyfish bitrate samples — h264 / hevc / hevc-10bit, HD + 4K.
BASE="https://repo.jellyfin.org/archive/jellyfish/media"
if want jellyfish; then
  fetch jellyfish/h264_10mbps.mkv    "$BASE/jellyfish-10-mbps-hd-h264.mkv"
  fetch jellyfish/hevc_10mbps.mkv    "$BASE/jellyfish-10-mbps-hd-hevc.mkv"
  fetch jellyfish/hevc10b_10mbps.mkv "$BASE/jellyfish-10-mbps-hd-hevc-10bit.mkv"
fi
if want 4k; then
  fetch 4k/h264_120mbps_4k.mkv    "$BASE/jellyfish-120-mbps-4k-uhd-h264.mkv"
  fetch 4k/hevc10b_120mbps_4k.mkv "$BASE/jellyfish-120-mbps-4k-uhd-hevc-10bit.mkv"
fi

echo "Done. Corpus at $DEST ($(du -sh "$DEST" 2>/dev/null | cut -f1))"
