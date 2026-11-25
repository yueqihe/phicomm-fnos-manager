# Project workflow

- Build FPK packages only when the user explicitly asks to package. Ordinary edits, including release preparation and copy changes, do not authorize packaging or a version bump. Building frontend assets and running appropriate checks are allowed.
- GitHub updates contain source, project assets and documentation only. Do not commit or upload FPK packages, signing metadata, developer certificates, certificate requests or private keys. Do not create a Release or upload release assets without a new explicit request.
- Keep user-facing introductions focused on supported devices and features. Put implementation details and development prerequisites in the development documentation.
- Retain the `phicomm-manager` application identifier and existing data compatibility. The display name is `IoT 设备管理`.
