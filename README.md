# CafeDrop v0.5.0 — Local Cybercafe File Intake

CafeDrop is a local-first file intake and session-management application for cybercafes. It runs on a Windows host and serves a mobile-friendly customer upload page over the local LAN.

## Requirements
- Go 1.23 or newer
- Windows host for the current folder-opening workflow
- Customer devices and host computer connected to the same isolated LAN
- A router or access point configured for the customer network

## Run

```powershell
go mod tidy
go build -o cafedrop.exe ./cmd/cafedrop
.\cafedrop.exe
```

Customer page: `http://<HOST-LAN-IP>:8080/`  
Admin page: `http://127.0.0.1:8080/admin`

Set a strong admin password before use with `CAFEDROP_ADMIN_PASSWORD`. Do not use the development fallback password in a live cybercafe.

## v0.4.0 network management
- Detects active network interfaces and their IPv4 addresses.
- Allows the operator to select and persist the LAN address advertised to customers.
- Shows the local customer URL and generates an offline QR image for it.
- Stores the Wi-Fi SSID and generates an open-network Wi-Fi QR code (`nopass`).
- Lists blocked IP addresses and lets the operator block or unblock an IP at the CafeDrop application layer.
- Keeps the existing session list, queue, upload flow, tracking codes, live upload notifications, and file-folder access.

## Captive Portal and router limits
The QR code can help a phone join an open Wi-Fi network and open the CafeDrop page. It does not configure the router or force an operating system to open a captive portal. Automatic redirection requires a compatible router/AP and a separate vendor-specific captive-portal configuration. IP blocking in this version only denies CafeDrop sessions and uploads; it does not deauthenticate a Wi-Fi station or block all network traffic. Devices behind the same NAT address may share one source IP, so blocking that IP can affect multiple devices.

## Security notes
- Keep the customer LAN isolated from the cybercafe's business/admin LAN.
- Do not expose port 8080 to the public Internet.
- Admin APIs require authentication.
- This prototype uses HTTP on the local network. Do not use it on an untrusted network without adding TLS and a separate protected management interface.
- The Wi-Fi QR feature in this release is for an open network only; it does not store or expose a Wi-Fi password.

## Data
SQLite database and uploaded files are stored under `data/` by default. Update scripts do not replace or delete this directory. Back it up independently before upgrades.


## v0.5.0 Admin dashboard update

This source update includes a redesigned administrator panel, automatic/manual light and dark themes, offline Noto Sans Arabic fonts, upload sorting and filters, upload deletion, and in-panel/audio/browser notifications.

### Update safely
1. Stop the currently running CafeDrop server.
2. Back up the existing `data` directory before updating.
3. Extract this update over the existing project directory and allow source files to be replaced.
4. Do **not** delete or replace the existing `data` directory. It contains the SQLite database and uploaded customer files.
5. Build the executable on the Windows host using the existing Go environment:
   `go build -o cafedrop.exe ./cmd/cafedrop`
6. Start CafeDrop and open `/admin`.

The update archive intentionally excludes `data/` and `cafedrop.exe`; it contains source files only.

### Notifications
Click **فعال‌سازی اعلان و صدا** once after opening the admin panel. This unlocks the audio alert and requests browser notification permission where supported. Browser system notifications require a secure context; on the host computer, use `http://localhost:8080/admin`. If the panel is opened from another device over plain HTTP, in-panel notifications and sound still work, but browser system notifications may be unavailable.

### Deleting uploads
Deletion requires administrator authentication and explicit confirmation. It removes the upload record and its associated stored folder. An upload still in progress is protected from deletion. Back up the data directory before using this destructive action.
