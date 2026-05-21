# OpenList/Alist Authentication Patterns

## Question

For OhMyCine Player's OpenList/Alist DataSource MVP, should the connection flow use username/password, token, public/guest access, path password, WebDAV basic auth, or another method?

## Sources Checked

* OpenList official API documentation: https://doc.oplist.org/api/apidocs
* OpenList official WebDAV guide: https://doc.oplist.org/guide/webdav.html
* AList archived API documentation: https://alist-archive-docs.pages.dev/guide/api/auth.html
* AList archived file API documentation: https://alist-archive-docs.pages.dev/guide/api/fs.html
* `github.com/imshuai/alistsdk-go` docs: https://pkg.go.dev/github.com/imshuai/alistsdk-go
* `python-alist-api` docs/source: https://python-alist-api.readthedocs.io/zh/stable/alist.component.admin.html and https://python-alist-api.readthedocs.io/zh/stable/_modules/alist/component/admin.html
* OpenList ecosystem page listing SDK/tools: https://doc.oplist.org/ecosystem.html

## Findings

### 1. HTTP API primary pattern: username/password login -> JWT token

OpenList's API docs expose `POST /api/auth/login` and the response contains a `token`. The same API documentation shows file operations such as `/api/fs/list`, `/api/fs/get`, and `/api/fs/search` under the file module.

AList's archived API docs show the same shape: `/api/auth/login` accepts username/password and returns a token. File APIs accept request bodies such as `path`, `password`, and pagination/sort fields.

Open-source SDKs mirror this pattern. `alistsdk-go` exposes `Login`, `SetToken`, and `GetToken`, then provides `FsList`, `FsGet`, `FsSearch`, etc. `python-alist-api` likewise includes token/auth helpers for admin/client operations.

Conclusion: for HTTP API integration, the most ecosystem-aligned model is:

1. User enters server URL + username + password.
2. Player calls `/api/auth/login`.
3. Player stores the returned token in secure credential storage.
4. Normal file requests send the token using Authorization.

### 2. Token-only should be supported as an advanced/manual path

Because SDKs expose `SetToken`/token-client style flows, token injection is common for programmatic clients. Token-only is useful when users do not want to save a password or already generated/copied a token.

Conclusion: token-only is a good secondary option, but should not be the only beginner flow.

### 3. Guest/public access exists but should not be the only MVP path

OpenList/Alist can expose public readable paths. The file API supports a `password` field in file requests, which is used for protected directories/files. Public/guest mode can work for read-only browsing and playback if the server permits it.

Conclusion: anonymous/public mode plus optional path password is useful, especially for shared libraries. However, it will not cover private libraries and should be treated as a separate "public access" mode.

### 4. WebDAV pattern: username/password basic auth

The OpenList WebDAV guide documents WebDAV access under `/dav`, with notes about authentication and compatible clients. In broader WebDAV clients, the common credential model is server URL + username + password/app password.

Conclusion: WebDAV should use username/password when implemented, but the Player roadmap currently lists WebDAV as a fallback/alternative for OpenList/Alist and a primary route for CloudDrive2.

### 5. Directory password is not account authentication

The `password` field on `/api/fs/list` and related APIs protects a specific path. It should be modeled separately from account credentials. A user may have no account login but still need to supply a path password for a shared folder.

Conclusion: design should separate:

* account credential: username/password or token
* path access password: optional per DataSource root or per request

## Recommendation for OhMyCine Player MVP

Product decision after discussion: OhMyCine Player's OpenList/Alist MVP will support **account login only**. Token-only and public/shared access are intentionally deferred.

Implementation target:

1. User enters server URL + username + password.
2. Player calls `/api/auth/login`.
3. Player stores the returned token in credential storage.
4. Ordinary `DataSourceConfig` stores only non-sensitive fields plus `extra.credentialRef`.
5. File API requests use the stored token.

Deferred:

* Manual token input.
* Public/shared directory access.
* Path password support.
* WebDAV mode.

## Broader Options Found

Use a three-mode connection design, implemented in this order:

1. **Account login (recommended default)**: URL + username + password -> `/api/auth/login` -> store token in credential store. Do not persist password unless needed for token refresh/re-login; if persisted, store only in credential storage.
2. **Token mode (advanced)**: URL + token -> store token in credential store. Useful for users who manage tokens manually.
3. **Public/shared mode**: URL + optional root path + optional path password. No account token required; send path password with fs requests where needed.

For the first implementation slice, prefer HTTP API over WebDAV:

* Better aligned with existing Player DataSource API (`list/search/getDetail/getStreamURL`).
* Supports OpenList/Alist file metadata and search endpoints directly.
* Leaves WebDAV available later as fallback and for CloudDrive2.

Security notes:

* Ordinary `DataSourceConfig` should store only non-sensitive fields and `extra.credentialRef`.
* Token/password/path password should be treated as sensitive.
* Logs and error messages must redact Authorization, token, password, and signed/download URLs.
* URL scheme should be restricted to `http`/`https` for HTTP API mode and WebDAV schemes only for WebDAV mode.
