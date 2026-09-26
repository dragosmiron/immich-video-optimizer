# Immich Video Optimizer CLI

A standalone, high-performance command-line tool designed to drastically reduce Immich server storage by re-encoding excessively large original videos using local hardware-accelerated HEVC (H.265) compression while faithfully preserving all camera metadata and timestamps.

---

## ✨ Features

- **Blistering Fast Hardware Acceleration:**
  - **macOS:** Apple Silicon / Intel VideoToolbox (`hevc_videotoolbox`) — **~7.3x realtime, 220+ FPS**.
  - **Linux:** Intel QuickSync (`hevc_qsv`), VA-API (`hevc_vaapi`), or Nvidia (`hevc_nvenc`), with CPU fallback (`libx265`).
- **Complete Camera & EXIF Preservation:**
  - Uses ExifTool to copy all camera make/model, shooting date/time, and GPS coordinates.
  - Automatically excludes internal track and rotation headers (`--Track*`, `--Rotation`) to prevent MP4 container corruption.
- **Immich Timestamp & File Integrity:**
  - Preserves original `fileCreatedAt`, `fileModifiedAt`, `deviceAssetId`, and original filenames in the Immich database.
- **Safety First:**
  - Originals are **never** deleted from the server unless the optimized replacement has uploaded and validated successfully.
- **Progress Tracking Album:**
  - Automatically creates a temporary tracking album (e.g. `Immich-Optimizer-InProgress-<timestamp>`) on your server and places both original and replaced videos inside it for easy web UI review.
- **Zero-Dependency Architecture:**
  - Compiled into a single standalone binary.
  - Automatically locates, extracts, or downloads standalone binaries for `ffmpeg` and `exiftool` on demand.

---

## 🚀 Quick Start

### 1. Build the Binary
```bash
go build -o immich-video-optimizer .
```

### 2. Run the Optimizer
```bash
./immich-video-optimizer
```

If run for the first time, an interactive terminal setup will prompt for:
1. **Immich Server URL:** (e.g. `http://192.168.1.100:2283` or `https://immich.yourdomain.com`)
2. **Immich API Key:** (Generate one in your Immich user settings)
3. **Size Threshold (MB):** (e.g. `500` to target videos $> 500$ MB)

Configuration is saved locally to `~/.config/immich-video-optimizer/config.yaml`.

---

## 🧪 Testing

Run the full automated test suite (including mock server and E2E integration tests):

```bash
go test -v ./...
```
