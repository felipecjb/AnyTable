# ANYTABLE AGENT BEHAVIORAL RULES & ARCHITECTURAL INVARIANTS

## 1. CORE OPERATIONAL DIRECTIVES
- **No Stubs / No Placeholders:** All files, GUI handlers, and packet structures must be fully implemented and syntactically valid.
- **Root Cause Fixes:** Never bypass environment failures by recommending OS upgrades. The host environment (Ubuntu 18.04 ARM with `glibc 2.27`) is an immutable constraint.
- **Packaging First:** Every code change affecting the server or client must remain strictly compatible with Debian packaging scripts (`debian/`) and the GitHub Actions matrix.

---

## 2. STRICT PLATFORM & COMPATIBILITY CONSTRAINTS

### A. Host Target (Transmitter: Ubuntu 18.04 ARM)
- **glibc Constraint:** Maximum supported C library version is **`glibc 2.27`**.
  - All server compilation must target `glibc 2.27` (e.g., compiled inside `arm64v8/ubuntu:18.04` Docker container or purely statically linked).
- **Architectures:** Support both `arm64` (`aarch64`) and `armhf` (ARMv7).
- **Display Target:** Primary local X11 display (`DISPLAY=:0`) with dynamic `.Xauthority` detection.

### B. Client Target (Receiver: Windows & Linux x86_64)
- **Windows:** Target Windows 10/11 x86_64 with DPI-awareness manifest to avoid blurry scaling.
- **Linux:** Package as a standard x86_64 `.deb` package alongside a portable binary.

---

## 3. GUI DECOUPLING & ZERO-OVERHEAD PERFORMANCE LAWS
- **Thread Isolation:** The graphical user interface (GUI) and the video streaming/input loop MUST execute on separate threads or separate processes.
- **No Performance Degradation:** Under no circumstances should GUI repainting, animations, or widget updates delay frame capture or network transmission.
- **Background Persistence (Tray Mode):**
  - Closing the GUI window must NOT terminate the server or client connection.
  - When the window is closed, hide the window to the System Tray.
  - While minimized/hidden in the tray, GUI rendering tick rate must drop to 0 FPS (idle).
- **Ping / Telemetry Protocol:**
  - Heartbeat packets must be sub-100-byte payloads sent at 1 Hz intervals to compute round-trip time (RTT).
  - The Ping calculation must be displayed cleanly in the GUI/HUD (e.g., `Ping: 8ms`) without impacting video streaming throughput.

---

## 4. PACKAGING & SYSTEM INTEGRATION LAWS
- All server deployments on Linux must be automated via `dpkg -i`:
  - `debian/postinst` must configure `systemd`, reload daemon, enable the service, add the user to `input`/`video` groups, configure `udev`, and add a firewall rule to `ufw` if present.
  - `debian/prerm` must cleanly stop and disable the systemd service before removal.
- The `systemd` service (`anytable-server.service`) must have restart policies (`Restart=always`, `RestartSec=5`) and wait for the graphical environment (`After=graphical.target`).