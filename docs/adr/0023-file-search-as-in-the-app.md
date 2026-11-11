# 0023. File search is a file command, as in the app

- Status: Accepted
- Date: 2026-11-11

## Context

The app's search has two tabs, Messages and Files; the Files tab finds attachments by name and content, with the same search syntax as messages plus `ext:` for the file type. The API has `POST /api/v4/teams/{team_id}/files/search` next to the post search. mm-cli had `post search` and, for files, only `file list` of one channel. `mmctl` has no file search.

## Decision

- `file search <query>` searches attachments in the selected team.
- The query and the flags repeat `post search`: the Mattermost search syntax, `--in`, `--from`, `-n`; `--ext` adds `ext:` to the query.
- The output, text and `--json`, is that of `file list`, so `file download` takes the ids it prints.
- It only reads.

## Consequences

- Both search tabs of the app have a command, each in the group of what it finds.
- A script moves from finding a file to downloading it without other requests.
