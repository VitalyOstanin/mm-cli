# Architecture decision records

Each record keeps one decision as it was made. A record's body is not rewritten later; a changed decision gets a new record that supersedes the old one, and only the old record's status changes.

| №    | Decision                                                                                      | Status             |
|------|-----------------------------------------------------------------------------------------------|--------------------|
| 0001 | [Record decisions as ADRs](0001-record-decisions.md)                                          | Accepted           |
| 0002 | [Go and the official API client](0002-go-and-official-client.md)                              | Accepted           |
| 0003 | [Sign in through the desktop-app SSO flow](0003-desktop-sso-login.md)                         | Accepted           |
| 0004 | [Rate limiter and no polling](0004-rate-limit-no-polling.md)                                  | Superseded by 0006 |
| 0005 | [Session storage](0005-session-storage.md)                                                    | Accepted           |
| 0006 | [Rate-limit state shared between runs, and no polling](0006-rate-limit-state-between-runs.md) | Accepted           |
| 0007 | [Followed threads and read state](0007-followed-threads.md)                                   | Accepted           |
| 0008 | [The help is the full reference for agents](0008-help-is-the-reference-for-agents.md)         | Accepted           |
| 0009 | [Names follow the Mattermost ecosystem](0009-names-follow-the-mattermost-ecosystem.md)        | Accepted           |
| 0010 | [No backward compatibility yet](0010-no-compatibility-guarantee-yet.md)                       | Accepted           |
| 0011 | [Scheduled messages as in the app](0011-scheduled-messages-as-in-the-app.md)                  | Accepted           |
| 0012 | [CI must pass at release, not on every commit](0012-ci-green-at-release.md)                   | Accepted           |
| 0013 | [Saved connections are servers](0013-saved-connections-are-servers.md)                        | Accepted           |
| 0014 | [A channel in another team is team:channel](0014-channel-address-team-colon-channel.md)       | Accepted           |
| 0015 | [Confirmation without a terminal is --confirm](0015-confirm-flag.md)                          | Accepted           |
| 0016 | [Reply and list-size flags as in mmctl](0016-post-flags-as-in-mmctl.md)                        | Accepted           |
| 0017 | [Bookmark fields as in the API](0017-bookmark-fields-as-in-the-api.md)                        | Accepted           |
| 0018 | [Pin and unpin are post actions, pinned messages a channel view](0018-pin-commands-where-the-app-has-them.md) | Accepted |
| 0019 | [Save and unsave are post actions](0019-save-commands-are-post-actions.md)                    | Accepted           |
| 0020 | [Marking as read is mark-read](0020-mark-read-commands.md)                                    | Accepted           |
