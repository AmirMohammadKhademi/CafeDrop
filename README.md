# CafeDrop v0.1.2 — Local File Intake

Local-first file intake for a cybercafe. This release provides a mobile upload page, streaming-to-disk storage, random tracking codes, SQLite metadata, and a custom admin login form.

## Requirements
- Go 1.23+
- A local LAN where customer devices can reach the host computer

## Run (Windows PowerShell)

```powershell
$env:CAFEDROP_ADMIN_USER = 'admin'
$env:CAFEDROP_ADMIN_PASSWORD = 'use-a-long-unique-password'
go mod tidy
go build -o cafedrop.exe ./cmd/cafedrop
.\cafedrop.exe
```

Open customer page: `http://127.0.0.1:8080/`
Open admin page: `http://127.0.0.1:8080/admin`

For a phone on the same LAN, use `http://<HOST-LAN-IP>:8080/`.

## Admin authentication
The `/admin` route serves a public static login shell. Admin data endpoints require HTTP Basic credentials sent explicitly by the page after the user submits the custom login form. Unauthorized API responses return JSON `401` and intentionally do not include `WWW-Authenticate`; this prevents the browser from opening its native username/password dialog. Credentials are held only in page memory and cleared on logout or page reload.

Set `CAFEDROP_ADMIN_USER` and `CAFEDROP_ADMIN_PASSWORD` in the same PowerShell session before starting the executable. Do not use weak sample passwords.

## Current scope
- Multi-file multipart uploads streamed to disk.
- Per-request sequential folder ID and random tracking code.
- SQLite metadata in WAL mode.
- Mobile-first Persian customer page.
- Custom admin login, recent upload list, tracking-code lookup, and ZIP download.

## Not implemented yet
- Session Manager, newest-kick eviction, waiting queue, and grace-period state machine.
- Router/AP management, DHCP/DNS, captive portal, Wi-Fi deauthentication, and firewall integration.
- Automated retention cleanup and installer/service packaging.

## Network and security
The server binds to `0.0.0.0:8080` by default. Do not expose this port to the public Internet. Use an isolated customer LAN and firewall rules that block customer devices from the main cybercafe LAN and admin endpoints. The current prototype uses HTTP; do not use it on an untrusted network. Add HTTPS or a separate protected management interface before production deployment.
