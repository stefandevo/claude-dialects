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

Claude Dialects binds embedded proxy instances to `127.0.0.1` and stores
configuration and provider credentials in owner-only local files. It is
intended for a single trusted user on a local macOS machine. Exposing a proxy
port to another machine, sharing an instance directory, or weakening its file
permissions is outside the supported security model.
