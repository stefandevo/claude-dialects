# Security policy

## Supported versions

Security fixes are made on the `main` branch. This project does not publish
versioned binaries or GitHub releases; users should rebuild from the latest
source after a security fix.

## Reporting a vulnerability

Please do not open a public issue for vulnerabilities, suspected credential
exposure, or authentication bypasses.

Use GitHub's private vulnerability reporting:

https://github.com/stefandevo/claude-dialects/security/advisories/new

Include the affected version, reproduction steps, impact, and any suggested
mitigation. You should receive an acknowledgement within seven days. Please
allow a reasonable period for investigation and remediation before public
disclosure.

## Security boundary

Claude Dialects is intended for one trusted user on a local macOS machine.
Embedded proxy instances and provider bridges bind to `127.0.0.1`, and
configuration and provider credentials are stored in owner-only local files.
Exposing a proxy or bridge port to another machine, sharing an instance
directory, or weakening its file permissions is outside the supported security
model.

The web dashboard is also local-only. `cc-dialect web` accepts only a numeric
loopback listener, such as `127.0.0.1:0` or `[::1]:0`; wildcard, LAN, hostname,
and non-loopback listeners are rejected before binding. Remote access, port
forwarding, containers that publish the port, and reverse-proxy deployment are
unsupported.

Every dashboard request must use a `Host` header exactly equal to the bound
listener authority, including its selected port. Read requests may omit
`Origin`, but any supplied origin must exactly match the dashboard origin.
State-changing API requests additionally require both the exact `Origin` and the
per-process `X-CC-Dialect-CSRF` token returned by `/api/v1/bootstrap`. The token
is kept in frontend memory rather than persistent browser storage. The server
does not enable cross-origin access and applies a same-origin content security
policy, frame denial, no-referrer, and content-type protections.

These controls mitigate browser-origin, DNS-rebinding, and cross-site request
attacks; they are not local user authentication. Another process running as the
same user can connect to loopback, fetch the bootstrap response, and construct a
valid request. Do not run the dashboard on a machine where other local processes
or users are untrusted.

Dashboard API responses use a secret-safe projection of state. They omit local
API keys, OAuth credential contents, upstream token values, extra environment
variable values, the Cursor API key, and native-launcher content digests.
Upstream URLs are stripped of user information, query strings, and fragments;
only environment-variable names, extra-variable key names, and key-presence
status are exposed.
