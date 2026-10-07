// Storage docs, shared by the Docs page (cards) and the full markdown
// export for LLMs, so both always say the same thing.

export interface StorageDocSection {
  title: string
  note?: string
  code: string
}

export const STORAGE_INTRO = `Files live in buckets. The rules — whether a bucket is public, its upload limits and who owns each file — are stored in Postgres, and rapibase serves every file itself (the S3 backend is never exposed):

- **Public bucket**: anyone can read its files at \`/api/v1/storage/public/{bucket}/{path}\`, no key needed (what goes in an \`<img src>\`). Uploading, replacing and deleting still require authorization.
- **Private bucket** (default): readable with the SERVICE_KEY, by the file's **owner** (ANON_KEY + the user's JWT), or through a **signed URL** (temporary, no keys).
- **Owner**: a file uploaded with ANON_KEY + JWT belongs to that user — only they can read it (private buckets), replace it or delete it. Service-key uploads have no owner unless you pass \`owner_id\`.
- **Limits per bucket**: \`file_size_limit\` (bytes) and \`allowed_mime_types\` (e.g. \`["image/*", "application/pdf"]\`). Uploads that break them get 413 / 415.
- **Access rules**: for anything beyond "owner or public" (team folders, chat participants, shared projects…) add SQL rules per bucket. They are real Postgres row-level security on the files, evaluated as the app user.
- Downloads support \`Range\` (seekable video/audio), \`ETag\` / 304 and caching. HTML and SVG are served sandboxed, so an uploaded file can never run script on your domain.`

export const STORAGE_SECTIONS: StorageDocSection[] = [
  {
    title: 'Buckets (service key)',
    code: `POST   /api/v1/storage/buckets
       { "name": "avatars", "public": true,
         "file_size_limit": 5242880, "allowed_mime_types": ["image/*"] }
GET    /api/v1/storage/buckets          → settings + object_count + total_size
GET    /api/v1/storage/buckets/{name}
PATCH  /api/v1/storage/buckets/{name}   { "public": false }   (only what you send changes)
DELETE /api/v1/storage/buckets/{name}?force=true   (force also deletes the files)
Headers: { "apikey": "SERVICE_KEY" }`,
  },
  {
    title: 'Upload',
    note: 'With ANON_KEY + JWT the file is owned by the user. upsert=false fails with 409 if the path is taken.',
    code: `POST /api/v1/storage/{bucket}/upload
Headers: { "apikey": "ANON_KEY", "Authorization": "Bearer <user-jwt>" }   // or SERVICE_KEY
Content-Type: multipart/form-data

Form Data:
  - file: (binary file)
  - path: "avatars/" (optional folder)  — or key: "avatars/me.png" (full path)
  - upsert: "false" (optional, default true)
  - cache_control: "86400" (optional, seconds; public buckets)
  - metadata: '{"product_id":"123"}' (optional JSON)
  - owner_id: "user-uuid" (service key only)

Response: {
  "id": "file-uuid", "bucket": "avatars", "key": "avatars/me.png",
  "size": 102400, "content_type": "image/png", "owner": "user-uuid",
  "url": "https://your-app/api/v1/storage/avatars/avatars/me.png",
  "public_url": "https://your-app/api/v1/storage/public/avatars/avatars/me.png"   // public buckets
}`,
  },
  {
    title: 'Public URL',
    note: 'Only for public buckets. No headers, cacheable by browsers and CDNs. Add ?download (or ?download=name.png) to force a download.',
    code: `GET /api/v1/storage/public/{bucket}/{path/to/file}

<img src="https://your-app/api/v1/storage/public/avatars/avatars/me.png">`,
  },
  {
    title: 'Signed URLs',
    note: 'Temporary keyless links to one file (max 7 days). Anyone who can read the file can create one.',
    code: `POST /api/v1/storage/sign/{bucket}/{path/to/file}
Body: { "expires_in": 3600, "download": "true" }   // both optional
→ { "signed_url": "https://your-app/api/v1/storage/sign/…?token=…", "expires_at": "…" }

POST /api/v1/storage/sign/{bucket}
Body: { "paths": ["a.png", "b.png"], "expires_in": 3600 }
→ { "urls": [ { "path": "a.png", "signed_url": "…" }, … ] }`,
  },
  {
    title: 'Signed uploads (browser → storage)',
    note: 'A logged-in user gets a one-file upload link (valid 2 hours, owned by them) and PUTs the file — no keys in the request.',
    code: `POST /api/v1/storage/upload/sign/{bucket}/{path/to/file}
Headers: { "apikey": "ANON_KEY", "Authorization": "Bearer <user-jwt>" }
Body: { "upsert": true }
→ { "signed_url": "https://your-app/api/v1/storage/upload/sign/…?token=…" }

PUT <signed_url>
Content-Type: image/png
<file bytes>          (or multipart/form-data with a "file" field)`,
  },
  {
    title: 'Access rules (service key)',
    note: 'SQL conditions checked for each file as the app user. Columns: bucket_id, name (full path), owner, size, mime_type, metadata. Functions: auth.uid(), auth.email(), auth.jwt(), storage.foldername(name), storage.filename(name), storage.extension(name) — plus any table the user can read (its own RLS applies). Permissive rules grant access on top of the built-in ones (public buckets; uploader owns their files while the bucket has owner_access on). Restrictive rules are extra requirements every access must meet.',
    code: `GET    /api/v1/storage/rules?bucket=docs
POST   /api/v1/storage/rules
DELETE /api/v1/storage/rules/{name}
Headers: { "apikey": "SERVICE_KEY" }

// Team folders: members read and upload files under "{team_id}/"
POST /api/v1/storage/rules
{ "name": "team_read", "bucket": "docs", "operation": "read",
  "condition": "EXISTS (SELECT 1 FROM team_members m WHERE m.user_id = auth.uid() AND m.team_id::text = (storage.foldername(name))[1])" }
{ "name": "team_upload", "bucket": "docs", "operation": "upload",
  "condition": "owner = auth.uid() AND EXISTS (SELECT 1 FROM team_members m WHERE m.user_id = auth.uid() AND m.team_id::text = (storage.foldername(name))[1])" }

// Only the files' own folder, on top of the default owner model
{ "name": "own_folder", "bucket": "avatars", "operation": "upload", "restrictive": true,
  "condition": "(storage.foldername(name))[1] = auth.uid()::text" }

// operation: read | upload | update (replace file or metadata) | delete | all
// Rely only on your rules: PATCH /api/v1/storage/buckets/docs { "owner_access": false }`,
  },
  {
    title: 'Download, list, delete',
    note: 'With ANON_KEY + JWT, listing a private bucket returns only the user\'s files; a public bucket lists everything.',
    code: `GET    /api/v1/storage/{bucket}/{path/to/file}      (supports Range / If-None-Match)
GET    /api/v1/storage/{bucket}/list?prefix=folder/&limit=100&offset=0
DELETE /api/v1/storage/{bucket}/{path/to/file}
POST   /api/v1/storage/{bucket}/remove   { "paths": ["a.png", "b.png"] }
POST   /api/v1/storage/{bucket}/move     { "from": "a.png", "to": "old/a.png" }
POST   /api/v1/storage/{bucket}/copy     { "from": "a.png", "to": "copy/a.png", "destination_bucket": "other" }`,
  },
  {
    title: 'Owners and metadata',
    code: `GET   /api/v1/storage/owner/{user_id}          (app users always get their own files)
GET   /api/v1/storage/{bucket}/search?key=product_id&value=123
GET   /api/v1/storage/{bucket}/metadata/{path/to/file}
PATCH /api/v1/storage/{bucket}/metadata/{path/to/file}
      { "metadata": { "product_id": "456", "featured": true } }`,
  },
  {
    title: 'JavaScript example',
    code: `const auth = { apikey: ANON_KEY, Authorization: 'Bearer ' + userJwt }

// upload the user's avatar (owned by them)
const form = new FormData()
form.append('file', file)
form.append('key', userId + '/avatar.png')
const res = await fetch(RAPIBASE_URL + '/api/v1/storage/avatars/upload', { method: 'POST', headers: auth, body: form })
const { public_url } = await res.json()   // public bucket → use it directly in <img>

// private bucket → temporary link
const signed = await fetch(RAPIBASE_URL + '/api/v1/storage/sign/invoices/' + path, {
  method: 'POST',
  headers: { ...auth, 'Content-Type': 'application/json' },
  body: JSON.stringify({ expires_in: 600 }),
}).then((r) => r.json())
window.open(signed.signed_url)`,
  },
]

export function storageMarkdown(): string {
  const fence = '```'
  return [
    '## Storage API',
    '',
    'Base URL: `/api/v1/storage`',
    '',
    STORAGE_INTRO,
    '',
    ...STORAGE_SECTIONS.flatMap((s) => [`### ${s.title}`, ...(s.note ? [s.note] : []), fence, s.code, fence, '']),
  ].join('\n')
}
