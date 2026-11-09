# 0017. Bookmark fields as in the API

- Status: Accepted
- Date: 2026-11-10

## Context

`bookmark add` and `bookmark edit` took the title as `--name` and the link as `--url`, and JSON output named them `"name"` and `"url"`. The app labels the fields Title and Link; the API names them `display_name` and `link_url`; `mmctl` has no bookmarks. For channels mm-cli already uses `--display-name`, as `mmctl` and the API do, while `--name` in `channel create` is the channel's address.

## Decision

- `bookmark add` and `bookmark edit` take `--display-name` and `--link-url`.
- JSON output names the fields `"display_name"` and `"link_url"`.

## Consequences

- A title is `display_name` for both channels and bookmarks, and flags match the JSON fields and the API.
- Scripts that pass `--name` or `--url`, or read `.name` and `.url`, stop working (see 0010).
