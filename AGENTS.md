# AnyTable: Agent & Contributor Technical Guide

This document defines the architecture, project structure, build constraints, and deployment specifications for autonomous AI agents and contributors working on **AnyTable**.

---

## 1. Project Overview

AnyTable is an ultra-low-latency LAN remote desktop system featuring lightweight GUIs on both ends:
* **Server (Transmitter):** Runs on **ARM Ubuntu 18.04 LTS (Bionic Beaver)**. Features a lightweight status/settings GUI and System Tray monitor, with the engine persisting silently in the background when the window is closed.
* **Client (Receiver):** Desktop viewer running on **Windows 10/11 (x86_64)** and **Ubuntu (x86_64)** with a real-time HUD displaying ping, FPS, and connection quality.
* **Packaging:** Native `.deb` packages for Ubuntu and standalone `.exe` binaries for Windows via GitHub Actions.

---

## 2. Directory Structure

```text
├── .agents/
│   └── rules                       # Agent operational invariants
├── .github/
│   └── workflows/
│       └── build.yml               # Multi-arch CI/CD pipeline
├── debian/
│   ├── control                     # Debian package metadata (ARM64/x86_64)
│   ├── postinst                    # Group, udev, ufw, and systemd setup
│   ├── prerm                       # Service teardown script
│   ├── anytable-server.service     # Systemd daemon definition
│   └── 99-anytable-uinput.rules    # Udev permissions for virtual input
├── server/
│   ├── src/
│   │   ├── capture/                # Low-level X11 screen capture
│   │   ├── input/                  # uinput and XTest injection
│   │   ├── network/                # WebSocket/TCP server and ping responder
│   │   ├── gui/                    # Lightweight configuration GUI & System Tray
│   │   └── main.go / main.cpp      # Server entry point
│   ├── config/                     # Default anytable-server.conf
│   └── Makefile / go.mod / Cargo   # Server build configuration
├── client/
│   ├── src/
│   │   ├── render/                 # Remote canvas renderer (OpenGL / DirectX)
│   │   ├── hud/                    # Real-time overlay (Ping indicator, FPS)
│   │   ├── input/                  # Mouse and keyboard event hooks
│   │   └── main.go / main.cpp      # Client entry point
│   ├── assets/                     # Windows app icon & manifests (DPI aware)
│   └── Makefile / go.mod / Cargo   # Client build configuration
├── common/
│   └── protocol/                   # Shared frame, input, and ping packet definitions
├── scripts/
│   ├── test_local_docker.sh        # Local Docker build simulation
│   └── run_server_debug.sh         # Manual debug script with DISPLAY hook
├── AGENTS.md                       # This architecture file
└── README.md                       # End-user documentation