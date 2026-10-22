# 🎬 Immich Video Optimizer

A terminal client for [Immich](https://immich.app/) that drastically reduces storage usage by re-encoding high-bitrate videos to HEVC (H.265) using local hardware acceleration, while flawlessly preserving original metadata, creation dates, GPS coordinates, album memberships, tags, stack membership, and favorite status.

---

## ✨ Features

- **Hardware Acceleration:** Native support for Apple Silicon & Intel (`hevc_videotoolbox`), NVIDIA (`hevc_nvenc`), and Intel Linux (`hevc_vaapi`), with automatic CPU fallback (`libx265`).
- **Full Metadata & State Preservation:** Preserves camera make/model, original shooting dates, GPS coordinates, original filenames, user album memberships, tags, stack membership, and favorite status.
- **Smart Filtering & Remote Probing:** Filter scans by bitrate density (`MB/min`), total file size, or date range (`YYYY-MM-DD`). Automatically detects and skips existing HEVC videos remotely without downloading them.
- **External Library Safety:** Automatically excludes read-only and external library assets from scans to protect external storage mounts.
- **Incremental Scans (Tagging):** Tags replaced videos (`immich-optimizer`) so previously optimized videos can be skipped on future scans.
- **Dry Run & Size Guard:** Preview estimated space savings before encoding. If a re-encoded video ends up larger than the original, replacement is skipped automatically.
- **Adjustable Quality:** 1–10 quality slider ranging from High Compression (~70–80% savings) to Visually Lossless (~15–20% savings), plus custom FFmpeg flags support.
- **Low-Footprint & Resumable:** Downloads, transcodes, and streams uploads in parallel while keeping at most 2 temporary videos on disk. Pause anytime and resume later where you left off.
- **Timestamped Gzip Log Rotation:** Automatically rotates logs when they exceed 10 MB and archives them to compressed `.log.gz` files to save disk space.
- **Universal Config Auto-Healing:** Automatically restores missing or deleted config items with defaults and repairs invalid settings on startup.

---

## 📖 Table of Contents

- [Features](#features)
- [Prerequisites](#prerequisites)
- [Installation](#installation)
  - [Option 1: Pre-Compiled Binary (Recommended)](#option-1-pre-compiled-binary-recommended)
  - [Option 2: Build From Source](#option-2-build-from-source)
- [Usage & Workflow](#usage--workflow)
  - [Step 1: Connect to Immich](#step-1-connect-to-immich)
  - [Step 2: Scan & Filter Candidates](#step-2-scan--filter-candidates)
  - [Step 3: Configure Settings](#step-3-configure-settings)
  - [Step 4: Dry Run & Savings Preview](#step-4-dry-run--savings-preview)
  - [Step 5: Run Batch Optimization](#step-5-run-batch-optimization)
  - [CLI Flags](#cli-flags)
- [Configuration](#configuration)
  - [Configuration File Locations](#configuration-file-locations)
  - [Annotated `config.yaml` Reference](#annotated-configyaml-reference)
  - [Universal Config Auto-Healing](#universal-config-auto-healing)
- [Logs & Diagnostics](#logs--diagnostics)
  - [Log File Locations](#log-file-locations)
  - [Checking & Inspecting Logs](#checking--inspecting-logs)
  - [Automatic Log Rotation & Gzip Archiving](#automatic-log-rotation--gzip-archiving)
- [Safety & Data Protection](#safety--data-protection)
  - [Zero-Data-Loss Pipeline](#zero-data-loss-pipeline)
  - [Tracking Albums & Verification](#tracking-albums--verification)
  - [Note for Mobile Users: "Free Up Space"](#note-for-mobile-users-free-up-space)
- [Development & Testing](#development--testing)
- [Reporting Issues](#reporting-issues)
- [Contributing](#contributing)
- [License](#license)

---

## 📋 Prerequisites

Immich Video Optimizer requires **`ffmpeg`** and **`exiftool`** installed and available in your system `PATH`:

- **macOS (Homebrew):**
  ```bash
  brew install ffmpeg exiftool
  ```

- **Linux (Debian / Ubuntu):**
  ```bash
  sudo apt update && sudo apt install -y ffmpeg libimage-exiftool-perl
  ```

- **Linux (Arch):**
  ```bash
  sudo pacman -S ffmpeg perl-image-exiftool
  ```

- **Windows (Winget / Scoop / Chocolatey):**
  ```powershell
  winget install Gyan.FFmpeg PhilHarvey.ExifTool
  # Or with Scoop: scoop install ffmpeg exiftool
  # Or with Chocolatey: choco install ffmpeg exiftool
  ```

---

## 📦 Installation

### Option 1: Pre-Compiled Binary (Recommended)

Download the latest pre-compiled archive for your OS and architecture from the [Releases](https://github.com/dragosmiron/immich-video-optimizer/releases) page:

| Platform | Architecture | Binary Archive |
|---|---|---|
| **macOS** | Apple Silicon (M1 / M2 / M3 / M4) | `immich-video-optimizer_darwin_arm64.tar.gz` |
| **macOS** | Intel x86_64 | `immich-video-optimizer_darwin_amd64.tar.gz` |
| **Linux** | 64-bit x86_64 | `immich-video-optimizer_linux_amd64.tar.gz` |
| **Linux** | ARM64 (Raspberry Pi 4/5, server) | `immich-video-optimizer_linux_arm64.tar.gz` |
| **Windows** | 64-bit x86_64 | `immich-video-optimizer_windows_amd64.zip` |

#### macOS / Linux Quick Start:
```bash
# 1. Extract the downloaded archive
tar -xzf immich-video-optimizer_darwin_arm64.tar.gz

# 2. Run the application
./immich-video-optimizer
```

> [!NOTE]
> **macOS Gatekeeper:** If macOS blocks the binary because it was downloaded from the browser, remove the quarantine flag:
> ```bash
> xattr -d com.apple.quarantine ./immich-video-optimizer
> ```
> Or right-click the binary in Finder → choose **Open** → click **Open Anyway**.

#### Windows Quick Start:
1. Extract `immich-video-optimizer_windows_amd64.zip`.
2. Open **Windows Terminal** or PowerShell in the extracted folder and run:
   ```powershell
   .\immich-video-optimizer.exe
   ```

---

### Option 2: Build From Source

Requirements: **Go 1.24+**, Git, `ffmpeg`, and `exiftool`.

```bash
git clone https://github.com/dragosmiron/immich-video-optimizer.git
cd immich-video-optimizer
go build -o immich-video-optimizer .
./immich-video-optimizer
```

---

## 🚀 Usage & Workflow

Launch `./immich-video-optimizer`. The application opens an interactive terminal interface with on-screen keyboard controls displayed at the bottom.

### Step 1: Connect to Immich
On first launch, enter your connection details:
- **Server URL:** Your Immich web address (e.g. `http://192.168.1.100:2283` or `https://immich.yourdomain.com`).
- **API Key:** Created in the Immich web UI under *Account Settings → API Keys*.

Credentials are saved securely in your local configuration file.

### Step 2: Scan & Filter Candidates
The optimizer queries your Immich library and filters videos based on your settings:
- **Bitrate Density (`MB/min`):** Catch high-bitrate clips (defaults to `100 MB/min`).
- **Total Size (`Total MB`):** Target files above a fixed size threshold.
- **Date Range:** Focus on older or newer footage (`YYYY-MM-DD`).
- **Smart Remote Probing:** Existing HEVC/H.265 videos are probed remotely and skipped without being downloaded.
- **Tag Exclusion:** Previously processed videos tagged with `immich-optimizer` are skipped automatically.

### Step 3: Configure Settings
Press `s` to open the Settings screen at any time:
- **Encoding Tab:** Adjust Video Quality (1–10 slider), Preset (Medium, Fast, Slow), Resolution (Original, 1080p, 720p), and Size Guard.
- **Filtering Tab:** Switch threshold modes, adjust size threshold, enable/disable HEVC skipping, or set date filters.
- **Pipeline Tab:** Adjust parallel encoders (1 or 2), toggle asset prefetching, network retry count, and logging level (`INFO` or `DEBUG`).
- **Advanced Tab:** Supply custom FFmpeg parameters and preview generated command lines live.

### Step 4: Dry Run & Savings Preview
Press `d` to execute a non-destructive dry run:
- Probes candidate videos remotely to calculate exact duration, bitrate, and codec.
- Creates a temporary Immich album (`Immich-Optimizer-DryRun-<TIMESTAMP>`) so you can visually inspect candidate videos in your Immich web/mobile app before encoding.
- Displays estimated disk space savings.

### Step 5: Run Batch Optimization
Press `Enter` to start optimization:
1. Downloads original video to a temporary staging buffer.
2. Transcodes to HEVC using local hardware acceleration.
3. Clones complete EXIF metadata and shooting date using ExifTool.
4. Uploads replacement video to Immich.
5. Verifies uploaded asset and restores original album memberships, favorite status, description, and tags.
6. Safely deletes the original video only after complete verification.
7. You can safely pause anytime (`Space` / `p`) and resume where you left off.

### CLI Flags

| Flag | Shorthand | Description |
|---|---|---|
| `--config` | `-c` | Open connection setup directly to reconfigure Immich URL or API key |
| `--version` | `-v` | Print version information and check for new GitHub releases |

---

## ⚙️ Configuration

### Configuration File Locations

The configuration file is automatically created and maintained in your user profile:

| Operating System | Path |
|---|---|
| **macOS & Linux** | `~/.config/immich-video-optimizer/config.yaml` |
| **Windows** | `%USERPROFILE%\.config\immich-video-optimizer\config.yaml` |

### Annotated `config.yaml` Reference

Here is a full `config.yaml` reference with all supported keys and their default values:

```yaml
# Immich Server Connection
url: "http://192.168.1.100:2283"
api_key: "your-api-key-here"

# Filtering & Candidate Selection
threshold_mode: "MB/min"       # Filter mode: "MB/min", "Total MB", or "Disabled"
size_threshold: "100"          # Bitrate density threshold (MB/min) or total file size (MB)
skip_hevc: true                # Skip assets already encoded in HEVC/H.265
exclude_optimized: true        # Exclude videos already tagged with 'immich-optimizer'
date_after: ""                 # Only optimize videos shot on or after (YYYY-MM-DD)
date_before: ""                # Only optimize videos shot on or before (YYYY-MM-DD)

# Encoding & Quality
resolution: "Original"         # Target resolution: "Original", "4K", "1080p", "720p", "480p"
preset: "Medium"               # Encoder speed preset: "UltraFast", "VeryFast", "Fast", "Medium", "Slow", etc.
video_quality: 6               # Quality slider: 1 (Max Savings) to 10 (Visually Lossless). Default: 6 (Balanced)
size_guard: true               # Abort replacement if transcoded file is larger than the original
custom_ffmpeg_flags: ""        # Optional custom FFmpeg flags (e.g. "-tune grain -color_primaries bt709")

# Concurrency & Network
concurrent_encoders: 1         # Number of parallel transcoding workers (1 recommended for HW encoders)
enable_prefetch: true          # Download the next video in background while current transcode runs
max_retries: 2                 # Retry attempts with exponential backoff for network operations

# Logging & Rotation
log_level: "info"              # Log level: "info" or "debug" (debug traces full FFmpeg stderr and commands)
log_max_size_mb: 10            # Maximum log file size in MB before rotating
log_max_backups: 5             # Maximum number of archived logs to retain
log_compress: true             # Compress rotated logs with gzip to save disk space
```

### Universal Config Auto-Healing

> [!TIP]
> **Zero Configuration Maintenance**:
> If you accidentally delete any line from `config.yaml` or misconfigure a value (e.g. setting negative numbers or invalid presets), Immich Video Optimizer automatically detects the missing or corrupted items on startup, restores them with their default values, and rewrites a clean, validated `config.yaml` to disk.

---

## 📑 Logs & Diagnostics

### Log File Locations

All application logs are stored alongside your configuration in:

| File | Description |
|---|---|
| `~/.config/immich-video-optimizer/optimizer.log` | Active operational log containing info, debug traces, and transcoding stats |
| `~/.config/immich-video-optimizer/failures.log` | Dedicated error log recording failed jobs, network issues, and error diagnostics |
| `~/.config/immich-video-optimizer/optimizer-<timestamp>.log.gz` | Timestamped, gzip-compressed rotated archives of past encoding sessions |

*(On Windows, replace `~` with `%USERPROFILE%`)*

### Checking & Inspecting Logs

You can monitor and inspect logs using standard terminal commands:

#### Live Log Tailing (Real-Time Monitoring)
To follow live transcoding activity while the optimizer runs in another terminal:
```bash
tail -f ~/.config/immich-video-optimizer/optimizer.log
```

#### Viewing Only Errors
To view the failure log:
```bash
cat ~/.config/immich-video-optimizer/failures.log
```

#### Searching & Viewing Compressed Archives
Rotated logs are compressed with gzip to save disk space. You can query them directly without unzipping:
```bash
# Search for specific errors across all compressed archives:
zgrep -i "error" ~/.config/immich-video-optimizer/optimizer-*.log.gz

# View a compressed archive in your terminal pager:
gzip -dc ~/.config/immich-video-optimizer/optimizer-2026-10-18T21-02-08.log.gz | less
```

### Automatic Log Rotation & Gzip Archiving

To prevent logs from consuming large amounts of disk space during massive library optimizations:
- **Size-Based Rotation:** When `optimizer.log` or `failures.log` reaches **10 MB**, it is automatically rotated.
- **Timestamped Naming:** The active file is archived to `optimizer-YYYY-MM-DDTHH-MM-SS.log.gz`. Previous archives are never renamed, keeping filenames and timestamps completely stable.
- **Gzip Compression:** Rotated archives are compressed using standard `gzip`, saving ~85–95% disk space (a 10 MB raw log compresses to ~500 KB – 1 MB).
- **Automatic Pruning:** Retains up to **5 archives** by default (`log_max_backups`), automatically removing the oldest archives.

---

## 🛡️ Safety & Data Protection

### Zero-Data-Loss Pipeline

Your media is protected through a multi-stage validation pipeline:
1. **Isolated Transcoding:** Transcoding runs in an isolated temporary directory.
2. **Metadata Parity:** ExifTool clones all original shooting dates, GPS tags, camera details, and color tags into the transcoded file.
3. **Atomic Verification:** The original video in Immich is deleted **only after**:
   - The transcoded file has successfully uploaded.
   - The new asset is verified to exist on Immich.
   - All original user album memberships are restored to the new asset.
   - Favorite status, descriptions, and tags are transferred.
4. **Failure Abort:** If any step fails or network drops occur, the original video is never deleted, and temporary files are cleaned up.
5. **External Library Protection:** External and read-only libraries are automatically excluded from scans to protect shared or mounted volumes.

### Tracking Albums & Verification

To give you visibility into what the optimizer is doing, temporary tracking albums are created on your Immich server:
- `Immich-Optimizer-DryRun-<YYYYMMDD-HHMMSS>`: Created during dry-run preview.
- `Immich-Optimizer-InProgress-<YYYYMMDD-HHMMSS>`: Live tracking album updated as videos are replaced.
- `Immich-Optimizer-Completed-<YYYYMMDD-HHMMSS>`: Renamed automatically when the batch finishes.

> [!TIP]
> **Album Cleanup:** Once you verify playback of your optimized videos in Immich, you can safely delete the tracking album. Deleting an album in Immich only deletes the album container—it does **not** delete the videos inside it or remove them from your personal albums.

### Note for Mobile Users: "Free Up Space"

If you use the Immich mobile app's **"Free up space"** feature:
> [!IMPORTANT]
> Run "Free up space" on your phone **before** optimizing videos on your server.
>
> Immich matches mobile device files to server assets using exact file checksums (SHA-1). Because re-encoding produces a new file with a different checksum:
> - The mobile app will treat the optimized video as cloud-only.
> - The original raw video on your phone will become unlinked, so "Free up space" will not automatically delete it from your local phone storage (you would need to delete it manually from your phone gallery).
> - The phone will **not** re-upload the original video.

---

## 🛠️ Development & Testing

Run the test suite and verify code hygiene:

```bash
# Run all tests with race detector
go test -v -race ./...

# Run static analysis
go vet ./...

# Build binary
go build -o immich-video-optimizer .
```

---

## 🐛 Reporting Issues

If you encounter a bug, crash, or unexpected behavior:

1. **Search existing issues:** Check [GitHub Issues](https://github.com/dragosmiron/immich-video-optimizer/issues) to see if the issue has already been reported.
2. **Open a new issue with details:**
   - **Environment:** OS, CPU architecture (`x86_64` / `arm64`), and Immich server version.
   - **Hardware & Drivers:** GPU/CPU model and active hardware encoder (e.g., Apple VideoToolbox, NVIDIA NVENC, Intel VAAPI, or CPU `libx265`).
   - **Logs:** Check `~/.config/immich-video-optimizer/failures.log` or `optimizer.log`. Failed transcoding steps automatically record the full FFmpeg command and stderr output in these logs.
   - **Verbose Debugging (optional):** Set `log_level: "debug"` in `~/.config/immich-video-optimizer/config.yaml` or press `[s]` in the app to open the Settings menu and switch **Logging Level** to `debug`.
   - **Media Info:** If an issue occurs on specific files, include the output of `ffprobe <file>` (feel free to redact private metadata).

---

## 🤝 Contributing

Contributions are welcome! Whether fixing bugs, adding support for new hardware encoders, optimizing performance, or improving documentation, your help is appreciated.

### Workflow

1. **Discuss first:** For significant features or breaking changes, please [open an issue](https://github.com/dragosmiron/immich-video-optimizer/issues) to discuss the plan before writing code.
2. **Fork and branch:** Create a descriptive branch from `main`:
   ```bash
   git checkout -b feature/my-new-feature
   ```
3. **Verify tests & hygiene:** Ensure your changes pass tests and lint checks:
   ```bash
   go test -v -race ./...
   go vet ./...
   ```
4. **Submit a Pull Request:** Open a PR against `main` describing what changed, why, and how you verified it.

---

## 📄 License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE).
