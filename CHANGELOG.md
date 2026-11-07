# CafeDrop v0.1.2

- Fixed admin authentication behavior: unauthorized admin API requests return JSON 401 without `WWW-Authenticate`, preventing the browser-native Basic Authentication popup.
- Kept the custom admin login form and protected admin API endpoints.
- No changes to upload storage or SQLite data layout.
