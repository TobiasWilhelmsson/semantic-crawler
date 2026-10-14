# Security

## Reporting

Report suspected vulnerabilities through [GitHub private vulnerability reporting](https://github.com/TobiasWilhelmsson/semantic-crawler/security/advisories/new).
Do not disclose an exploit or sensitive data in a public issue. Include the
version, affected command or route, impact and a minimal sanitized reproduction.

The maintainer supports the current development revision. There are no published
version support guarantees or promised response times yet.

## Current boundary

The review interface is for a trusted local operator and binds to loopback by
default. It has host/origin checks and CSRF protection, but no user accounts,
authentication, tenant separation or hosted access policy. Approving a hostname
does not provide authentication. A shared deployment needs additional controls.

Source HTML and model responses are untrusted inputs. Crawl URLs are bounded by
the configured hostname/path, and model responses are schema-validated before
acceptance. Source snapshots block scripts/forms; permitted HTTPS images/styles
can still contact third parties. Selected HTML in structured exports is source
material, not a sanitized executable document.

Live extraction sends source blocks, scope, instructions and active reviewer
rules to the configured provider. Keep keys in the ignored local environment
file. Review the provider's handling of the information you process.

SQLite contains snapshots, raw responses, instructions and review diagnostics;
it can hold sensitive data. Protect local files and backups. Stop related
processes before copying a database and any SQLite sidecars, together with source
configs and instructions. Report files and exports also need appropriate access.

HTTP delivery is approved-only, allowlisted and refuses redirects. It does not
include built-in authentication or deduplication. An ambiguous failed request
may have reached its receiver. Local filesystem locking and output checks assume
a trusted operator and stable database paths; they are not a boundary against a
hostile local process.

See [provider configuration](docs/providers.md), [review and recovery](docs/semantic_reviewer_handbook.md)
and [export limits](docs/output-formats.md). [Dated verification](docs/verification.md)
records the checks actually performed; it does not establish hosted security.
