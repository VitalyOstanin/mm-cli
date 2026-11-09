# 0003. Sign in through the desktop-app SSO flow

- Status: Accepted
- Date: 2026-11-09

## Context

Many Mattermost servers allow sign-in only through SSO (SAML, GitLab, OpenID Connect, Google, Office 365), often with a second factor, and do not let users create personal access tokens. A command-line client cannot show the identity provider's pages itself.

The desktop app solves the same problem: it opens the system browser at `/login/sso/<method>?desktop_token=<client token>`; after sign-in the server redirects the browser to `/login/desktop?client_token=...&server_token=...`; the app exchanges the server token for a session at `POST /api/v4/users/login/desktop_token`. The server token is valid for 3 minutes.

## Decision

`mm-cli auth login` uses this flow. mm-cli generates a random 32-byte client token, opens the sign-in page (or prints it with `--no-browser`) and asks for the `/login/desktop` address. The pasted address must point at the configured server's host and carry the same client token, compared in constant time; then the server token is exchanged for a session.

For use without a terminal the login is split in two steps: the first stores the pending client token in `sessions/<profile>.pending` (mode 0600, rejected after 30 minutes) and prints the command for the second step, `mm-cli auth login --callback '<address>'`.

`--token-stdin` remains as a fallback for servers with tokens or for automation.

## Consequences

- Works with any SSO method the server offers and with second factors, without tokens or stored passwords.
- The user copies one address by hand. When the browser already has a Mattermost session, the web app leaves `/login/desktop` at once, so the address has to be taken from the browser history; the help text says so.
- The flow depends on server endpoints made for the desktop app; if they change, login breaks until mm-cli follows.
