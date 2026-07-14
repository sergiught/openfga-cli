# Security Policy

## Supported versions

ofga is pre-1.0; only the latest tagged release receives security fixes. Pin a
tagged version and upgrade to pick up fixes.

| Version        | Supported          |
| -------------- | ------------------ |
| latest release | :white_check_mark: |
| older          | :x:                |

## Reporting a vulnerability

**Please do not open a public issue for security reports.**

Report privately via GitHub's private vulnerability reporting:

→ https://github.com/sergiught/openfga-cli/security/advisories/new

We aim to acknowledge reports within **5 business days** and provide a status
update within **10 business days**. Coordinated disclosure is appreciated —
we'll agree on a timeline before any public detail is shared.

## Scope

ofga is a command-line client and TUI for an OpenFGA server. The most relevant
classes of issue are:

- Mishandling of credentials — API tokens, client-credentials secrets, or
  private keys read from config, environment, or flags leaking into logs,
  error messages, request URLs, or the on-disk config file.
- Sending credentials over cleartext transport, or incorrect TLS handling that
  could expose requests to interception.
- Request construction flaws (header injection, request smuggling) reachable
  from caller-supplied input.
- Unsafe handling of untrusted server responses in the CLI/TUI (e.g. terminal
  escape-sequence injection from model or tuple data rendered to the screen).

Out of scope:

- Vulnerabilities in the OpenFGA **server** itself — report those to the
  [OpenFGA project](https://github.com/openfga/openfga).
- Issues that require a malicious OpenFGA server you already fully control; the
  client trusts the server it is configured to talk to.
- Vulnerabilities in third-party dependencies that are already public — though
  we do appreciate a heads-up so we can bump them.
