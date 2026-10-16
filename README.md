# 🎬 Immich Video Optimizer

A terminal client for [Immich](https://immich.app/) that reduces storage usage by re-encoding videos to HEVC (H.265) using local hardware acceleration, while preserving original metadata, creation dates, GPS coordinates, album memberships, tags, stack membership, and favorite status.

---

## ✨ Features

- **Hardware Acceleration:** Apple Silicon & Intel (`hevc_videotoolbox`), NVIDIA (`hevc_nvenc`), and Intel Linux (`hevc_vaapi`), with CPU fallback (`libx265`).
- **Full Metadata & State Preservation:** Preserves camera make/model, original shooting dates, GPS coordinates, original filenames, user album memberships, tags, stack membership, and favorite status.
- **Smart Filtering & Remote Probing:** Filter scans by bitrate density (`MB/min`), total file size, or date range (`YYYY-MM-DD`). Automatically detects and skips existing HEVC videos without downloading them.
- **External Library Safety:** Automatically excludes read-only and external library assets from scans to protect external storage mounts.
- **Incremental Scans (Tagging):** Tags replaced videos (`immich-optimizer`) so previously optimized videos can be skipped on future scans.
- **Dry Run & Size Guard:** Preview estimated space savings before encoding. If a re-encoded video ends up larger than the original, replacement is skipped automatically.
- **Adjustable Quality:** 1–10 quality slider ranging from High Compression (~70–80% savings) to Visually Lossless (~15–20% savings).
- **Low-Footprint & Resumable:** Downloads, transcodes, and streams uploads in parallel while keeping at most 2 temporary videos on disk. Pause anytime and resume later where you left off.

---

## 📦 Installation

### Option 1: Pre-Compiled Binary (Recommended)

Download the latest release for your platform from the [Releases](https://github.com/dragosmiron/immich-video-optimizer/releases) page:

| Platform | Architecture | Binary Archive |
|---|---|---|
| **macOS** | Apple Silicon (M1 / M2 / M3 / M4) | `immich-video-optimizer_darwin_arm64.tar.gz` |
| **macOS** | Intel x86_64 | `immich-video-optimizer_darwin_amd64.tar.gz` |
| **Linux** | 64-bit x86_64 | `immich-video-optimizer_linux_amd64.tar.gz` |
| **Linux** | ARM64 (Raspberry Pi 4/5, servers) | `immich-video-optimizer_linux_arm64.tar.gz` |
| **Windows** | 64-bit x86_64 | `immich-video-optimizer_windows_amd64.zip` |

#### macOS / Linux Quick Start:
```bash
# 1. Extract the downloaded archive
tar -xzf immich-video-optimizer_darwin_arm64.tar.gz

# 2. Run the application
./immich-video-optimizer
```

> **Note for macOS:** If Gatekeeper blocks the downloaded binary, remove the quarantine attribute:
> ```bash
> xattr -d com.apple.quarantine ./immich-video-optimizer
> ```
> Or right-click the binary in Finder → choose **Open** → click **Open Anyway**.

#### Windows Quick Start:
1. Install prerequisites using **Winget**:
   ```powershell
   winget install Gyan.FFmpeg PhilHarvey.ExifTool
   ```
   *(Or using Scoop: `scoop install ffmpeg exiftool` or Chocolatey: `choco install ffmpeg exiftool`)*

2. Extract `immich-video-optimizer_windows_amd64.zip` and run the binary in **Windows Terminal** or PowerShell:
   ```powershell
   .\immich-video-optimizer.exe
   ```

---

### Option 2: Build From Source

Requirements: **Go 1.24+**, Git, `ffmpeg`, and `exiftool` in your `PATH`:
- **macOS:** `brew install ffmpeg exiftool`
- **Linux (Debian/Ubuntu):** `sudo apt update && sudo apt install -y ffmpeg libimage-exiftool-perl`
- **Windows:** `winget install Gyan.FFmpeg PhilHarvey.ExifTool`

```bash
git clone [https://github.com/dragosmiron/immich-video-optimizer.git](https://github.com/dragosmiron/immich-video-optimizer.git)
cd immich-video-optimizer
go build -o immich-video-optimizer .
./immich-video-optimizer
```

---

## 🚀 Usage

Launch `./immich-video-optimizer` (pass `--config` or `-c` anytime to update your server URL or API key). All keyboard shortcuts are shown at the bottom of the terminal UI.

1. **Connect:** Enter your Immich URL (e.g. `http://192.168.1.100:2283`) and **API Key** (created in Immich under *Account Settings → API Keys*). Saved to `~/.config/immich-video-optimizer/config.yaml`.
2. **Scan & Tune:** Review videos exceeding your threshold (defaults to `100 MB/min` to catch high-bitrate clips). Open **Settings** (organized into `Encoding`, `Filtering`, and `Pipeline` tabs) to customize quality (1–10), resolution, set date ranges, or exclude already-optimized videos. Run a **Dry Run** to preview estimated savings.
3. **Optimize:** Start the batch and monitor progress in real time. You can safely pause at any time and resume the session later.

---

## 📱 Note for Mobile Users: "Free Up Space"

If you use the Immich mobile app's **"Free up space"** feature, run it on your phone **before** optimizing videos on your server.

Immich matches phone files to server assets using exact file checksums (SHA-1). Because re-encoding produces a new checksum:
- The mobile app will display the optimized video as cloud-only.
- The original video on your phone will become unlinked, so "Free up space" won't delete it automatically (you would need to delete it manually from your gallery app). The phone will not re-upload it.

If you don't use the "Free up space" feature, you can ignore this.

---

## 🗂️ Tracking Albums & Safety

### Zero-Data-Loss Replacement
An original video on Immich is deleted **only after** the optimized replacement has finished uploading, been re-added to all your original albums, and had its favorite status restored. If any step fails, deletion is aborted and your original video remains untouched. Videos in external or read-only libraries are automatically skipped during scans.

### How Tracking Albums Work
To help you verify your videos in Immich, the app creates a temporary tracking album on your server:
- **Dry Run:** `Immich-Optimizer-DryRun-<YYYYMMDD-HHMMSS>` (lets you preview candidate videos in Immich before encoding).
- **In Progress:** `Immich-Optimizer-InProgress-<YYYYMMDD-HHMMSS>` (updated live as optimized videos replace originals).
- **Completed:** `Immich-Optimizer-Completed-<YYYYMMDD-HHMMSS>` (renamed once the batch finishes).

> **Cleanup Tip:** Once you check playback of your optimized videos in the Immich web or mobile app, **delete the tracking album in Immich**. Deleting an album in Immich only removes the album container—it does **not** delete the videos inside it or remove them from your personal albums.

---

## 📄 Troubleshooting & License

- **Logs:** `~/.config/immich-video-optimizer/optimizer.log`
- **Tests:** `go test -v ./... && go vet ./...`
- **License:** [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE)
