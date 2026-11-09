# 0022. The browser finishes the SSO login through a mattermost-dev:// handler

- Status: Accepted
- Date: 2026-11-10

## Context

0003 makes the user copy the `/login/desktop` address into the terminal. When the browser already has a Mattermost session, the web app leaves that page at once for the default channel, and the address is only in the browser history; the first real login took three attempts because of it.

The web app's page (`webapp/channels/src/components/desktop_auth_token.tsx`, Mattermost 12.1) does one more thing in every case, with or without a session: it replaces the scheme of its own address with `mattermost:` and opens it, so that the desktop app receives the tokens; with `isDesktopDev` in the address the scheme is `mattermost-dev:` instead. The server adds `isDesktopDev=true` when the client token starts with `dev-` (`server/channels/web/saml.go`, the same for OAuth), and otherwise only hands the client token back. `mattermost://` belongs to the desktop app; `mattermost-dev://` only to its development builds.

## Decision

- The client token is `dev-` followed by 60 hex digits, so that the page hands the address to `mattermost-dev://`.
- `mm-cli auth handler install` registers the running binary as the handler of `mattermost-dev://` links: a desktop entry and `xdg-mime` on Linux, a key under `HKCU\Software\Classes` on Windows, an AppleScript applet built with `osacompile` in `~/Applications` on macOS (macOS hands a URL to an application as an Apple Event, not as an argument). `auth handler remove` undoes it. Nothing is registered implicitly.
- The handler runs `mm-cli auth login --callback <link>`. Without a server name, `--callback` finds the started login by the link's client token among the pending logins.
- The pending login keeps the `--team` of `auth login`, which the handler's process applies.
- The waiting `auth login` on a terminal checks once a second, in local files only, whether the pending login is gone and the session changed, and then reports the login. Pasting the address keeps working, also as a `mattermost-dev://` address.

## Consequences

- With the handler registered the login needs no copying, also when the browser is already signed in.
- The client token is still compared in constant time, so a link opened by another page cannot finish a login it did not start.
- On a machine with a development build of the desktop app, `install` takes `mattermost-dev://` over.
- The flow now also depends on the web app opening the `mattermost-dev://` link; if that changes, pasting the address still works.
- The macOS registration builds an applet with system tools (`osacompile`, `plutil`, `codesign`, `lsregister`); it is checked by tests of the commands it runs, not on a Mac in CI.
