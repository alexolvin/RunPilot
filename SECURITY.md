# Security

RunPilot is a local, self-hosted tool. It binds to loopback / private
networks (Tailscale) by default, does not collect telemetry, and keeps all
data (database, tokens) on your own machines.

## Supported versions

Only the latest `main` is supported. Older revisions are not patched.

## Reporting a vulnerability

Please do **not** open a public issue for a security concern.

1. Prefer **GitHub → Security → Reporting a vulnerability** (private
   advisory) if the repository has it enabled.
2. Otherwise, contact the maintainer directly and give us time to respond
   before any public disclosure.

Include: a summary, reproduction steps, affected version, and any
mitigations you found.

## Security-relevant configuration

- `RUNPILOT_TOKEN` — operator credential. Keep it secret; it is read from the
  environment, never stored in the repo.
- `coordinator.allow_cidrs` — the IP allowlist for the coordinator API. Keep
  it to loopback / your overlay network.
- `coordinator.bind` — the coordinator bind address. Keep it on loopback or
  your overlay network, not a public interface.
- The coordinator API is intended for local use; expose it only through a
  trusted private network (e.g. Tailscale).

## Scope

In scope: the coordinator HTTP/WS API, the gateway, and the node. Out of
scope: vulnerabilities in third-party dependencies (report upstream first).
