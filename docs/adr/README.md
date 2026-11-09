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
