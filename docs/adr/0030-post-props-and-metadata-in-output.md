# 0030. Post props and metadata in output

- Status: Accepted
- Date: 2026-11-16

## Context

Bots and integrations post "message attachments" (cards) in `props.attachments`: colour, title with link, fields, footer, buttons. mm-cli printed only `message`, so a card post looked empty in text and its content was missing from `--json`. Other server data was lost too: the post type, the edit time, link previews, priority and acknowledgements.

## Decision

- `--json` carries `props` verbatim, with every key and nothing removed, so an agent sees the same data the app renders. Omitted when empty.
- Selected metadata is surfaced as content: `embeds` (`type`, `url`, `data` as sent), `priority`, `acknowledgements` (with user names), plus `type` and `edit_at`. Files and reactions keep their fields.
- Metadata that only hints at rendering is not surfaced: image dimensions and previews, emoji, translations.
- Text output prints message attachments under the message (`[attachment #color]`, pretext, author, title, text, fields, images, actions, footer), a plain `fallback` when nothing else is set, link previews and quoted messages, priority marks and acknowledgements. Bot, webhook, overridden user name and post type are marked in the header. Interactive blocks and cards print one line pointing to `props` in `--json`.
- `render.PlainText` gives the text of a post, using the first attachment when the message is empty; desktop notifications and reaction excerpts use it.
- Creating attachments (`send`, `post create`) is out of scope.

## Consequences

- Cards from bots are readable in text and complete in JSON; the format of `props` is the server's, not a mm-cli contract.
- JSON of messages gains optional fields; consumers that ignore unknown fields are unaffected.
- In `watch --json` the line field `type` keeps meaning `post` or `reaction`; the post type is `post_type`, left out for a normal message.
- Acknowledgement and quoted-message authors are looked up in the same name request as post authors.
