# Architecture decision records

Each record keeps one decision as it was made. A record's body is not rewritten later; a changed decision gets a new record that supersedes the old one, and only the old record's status changes.

| №    | Decision                                                                                                      | Status                                                |
|------|---------------------------------------------------------------------------------------------------------------|-------------------------------------------------------|
| 0001 | [Record decisions as ADRs](0001-record-decisions.md)                                                          | Accepted                                              |
| 0002 | [Go and the official API client](0002-go-and-official-client.md)                                              | Accepted                                              |
| 0003 | [Sign in through the desktop-app SSO flow](0003-desktop-sso-login.md)                                         | Accepted                                              |
| 0004 | [Rate limiter and no polling](0004-rate-limit-no-polling.md)                                                  | Superseded by 0006                                    |
| 0005 | [Session storage](0005-session-storage.md)                                                                    | Superseded by 0025                                    |
| 0006 | [Rate-limit state shared between runs, and no polling](0006-rate-limit-state-between-runs.md)                 | Accepted                                              |
| 0007 | [Followed threads and read state](0007-followed-threads.md)                                                   | Accepted                                              |
| 0008 | [The help is the full reference for agents](0008-help-is-the-reference-for-agents.md)                         | Accepted                                              |
| 0009 | [Names follow the Mattermost ecosystem](0009-names-follow-the-mattermost-ecosystem.md)                        | Accepted                                              |
| 0010 | [No backward compatibility yet](0010-no-compatibility-guarantee-yet.md)                                       | Accepted                                              |
| 0011 | [Scheduled messages as in the app](0011-scheduled-messages-as-in-the-app.md)                                  | Accepted                                              |
| 0012 | [CI must pass at release, not on every commit](0012-ci-green-at-release.md)                                   | Accepted                                              |
| 0013 | [Saved connections are servers](0013-saved-connections-are-servers.md)                                        | Accepted                                              |
| 0014 | [A channel in another team is team:channel](0014-channel-address-team-colon-channel.md)                       | Accepted                                              |
| 0015 | [Confirmation without a terminal is --confirm](0015-confirm-flag.md)                                          | Accepted                                              |
| 0016 | [Reply and list-size flags as in mmctl](0016-post-flags-as-in-mmctl.md)                                       | Accepted                                              |
| 0017 | [Bookmark fields as in the API](0017-bookmark-fields-as-in-the-api.md)                                        | Accepted                                              |
| 0018 | [Pin and unpin are post actions, pinned messages a channel view](0018-pin-commands-where-the-app-has-them.md) | Accepted                                              |
| 0019 | [Save and unsave are post actions](0019-save-commands-are-post-actions.md)                                    | Accepted                                              |
| 0020 | [Marking as read is mark-read](0020-mark-read-commands.md)                                                    | Accepted                                              |
| 0021 | [User-Agent is set per server](0021-user-agent-per-server.md)                                                 | Accepted                                              |
| 0022 | [The browser finishes the SSO login through a mattermost-dev:// handler](0022-browser-finishes-sso-login.md)  | Accepted                                              |
| 0023 | [File search is a file command, as in the app](0023-file-search-as-in-the-app.md)                             | Accepted                                              |
| 0024 | [Reactions: post reaction add, remove, list and watch --reactions](0024-reactions-to-your-messages.md)        | Accepted; reaction catch-up frequency refined by 0027 |
| 0025 | [Session token in the OS secret store](0025-session-token-in-os-secret-store.md)                              | Accepted; account naming superseded by 0026           |
| 0026 | [The secret-store account names the server and its address](0026-token-account-names-server-and-url.md)       | Accepted                                              |
| 0027 | [Reaction catch-up runs at most once per 10 minutes](0027-reaction-catch-up-at-most-every-10-minutes.md)      | Accepted                                              |
| 0028 | [In watch --json, "channel" is a channel address](0028-watch-channel-is-an-address.md)                        | Accepted                                              |
| 0029 | [Watch in the background as a systemd user service](0029-watch-in-the-background-as-a-systemd-user-service.md)| Accepted                                              |
