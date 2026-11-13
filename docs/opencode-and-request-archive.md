# OpenCode routing and Google Drive request backups

## OpenCode proxy

In a trusted Compose network, enable the `opencode` profile and set `OPENCODE_PROXY_URL=http://opencode-proxy:6446` and a private `OPENCODE_PROXY_KEY` of at least 24 characters. Add the OpenCode provider with `https://opencode.ai/zen/v1` as its base URL. Model discovery and inference go through the private sidecar; Rotakey does not send the provider's key to it. The sidecar does not publish a host port.

## Set up Google Drive

1. In Google Cloud, enable the Drive API and create an OAuth client of type **Web application**. Add the exact redirect URI shown in **Rotakey → Settings → Google Drive backup** to Authorized redirect URIs. Production requires the configured public HTTPS URL. Use an OAuth consent configuration that supports persistent access; Google may expire refresh tokens for external apps left in Testing.
2. Enter the client ID and secret in Rotakey and use **Connect Google account**. Choose the intended account and grant access. Listing and selecting existing writable folders uses the Drive scope; Rotakey stores the OAuth secret and refresh token encrypted and does not include them in settings responses or configuration export.
3. Browse or search for a writable folder and choose **Input/output**, **Metadata**, or both. The global **Save all request data** switch controls capture of new model requests independently of backup selection. Legacy rows sometimes have no saved bodies or have truncated bodies; the backup cannot recover data that was never stored.
4. Optionally turn on daily backups and pick a time in **Asia/Dhaka**. Enable **Delete after verified upload** only if scheduled backups should clean up the VPS. Save and choose **Upload only**, **Upload and delete**, or **Cancel** for the first run. Cancel skips that one-time backup; any saved daily schedule still applies. Deploying or connecting alone does not upload anything.

Manual runs are available through **Back up now**. **Backup activity** shows the latest runs, uploaded and cleaned counts, errors, and a link to the latest file. Backups run in the background and resume after app restarts. Downloads are compressed JSON Lines (`.jsonl.gz`). Input and output are stored as original strings, including streamed output; non-UTF-8 bodies are encoded as base64. Use a private folder: files can contain user conversation and model output.

Upload-only backups are incremental for the same destination and selection. A cleanup run verifies a fresh copy of all selected rows up to its cutoff, before deleting a single row or body. Rotakey rechecks checksums, file size, folder, and local row fingerprints. With both categories selected, verified complete rows are removed. With input/output only, the request row and its metadata stay. With metadata only, records with saved bodies stay to protect those bodies; verified metadata-only rows can be removed. Rows created after the cutoff or modified during backup are kept for a later run. Cleanup does not alter provider settings, keys or stored spend. PostgreSQL can reuse freed space; ordinary deletion does not immediately shrink the database files.

There is no Supabase transfer or scheduled Supabase service in this version. `PRESERVE_REQUEST_LOGS=true` can pause ordinary age-based retention during migration so existing data stays available until the operator configures and runs a verified backup.
