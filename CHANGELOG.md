# Changelog

## v0.5.0 — Admin Experience Redesign
- Rebuilt the admin dashboard visual system with responsive light and dark themes.
- Added automatic light/dark theme selection based on the admin browser's local time (light 07:00–18:59, dark 19:00–06:59), plus manual theme selection.
- Bundled Noto Sans Arabic fonts locally for offline Persian UI rendering.
- Added upload sorting by newest, oldest, largest, and smallest; date and status filters; and clearer local date/time display.
- Added authenticated deletion of an upload request and its stored files, with confirmation and path validation. Uploads still in progress cannot be deleted.
- Added in-panel toast notifications, a short three-tone audio alert, upload-row highlighting, and optional browser system notifications.
- Preserved existing admin, network, QR, session, search, download, and folder-opening features.

## v0.4.0 — Network & Access Management
- Added LAN interface discovery and private IPv4 candidate detection, preferring physical Wi-Fi/Ethernet adapters over common virtual adapters for automatic selection.
- Added persistent preferred host IP and Wi-Fi SSID settings.
- Added offline QR generation for the customer portal URL and open Wi-Fi network.
- Added a network status dashboard with 30-second refresh.
- Added application-level IP block/unblock controls and a persistent blocked-IP list.
- Blocked IPs cannot start sessions, heartbeat, or upload files; existing sessions from a blocked IP are terminated.
- Added a Captive Portal readiness note clarifying that router-side setup is still required.
- Preserved upload, sequential tracking codes, FIFO session queue, grace period, folder opening, and live upload notifications.

## v0.3.3 — Search, file handling, and live notifications
- Corrected stored file naming and original extension handling.
- Added partial tracking-code search.
- Added live upload events and desktop browser notifications.

## v0.3.2 — Tracking and folder access
- Added short sequential tracking codes across server runs.
- Added direct host-folder opening and path copy in the admin panel.

## v0.3.1 — UI refinement
- Refined customer and admin interface styling and responsive layout.

## v0.3.0 — Session Manager
- Added configurable concurrent sessions, session duration, upload grace period, FIFO waiting queue, and admin session actions.

## v0.2.0 — Upload administration
- Added original filenames, file sizes, individual file download, ZIP download, and storage-folder visibility.

## v0.1.2 — Admin authentication fix
- Fixed custom admin login behavior and prevented the browser-native Basic Authentication prompt.
