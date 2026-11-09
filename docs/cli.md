# Command reference

Generated from `mm-cli <command> --help` by `scripts/gen-cli-doc.sh`; do not edit by hand.

## Contents

- [Global flags](#global-flags)
- [auth](#auth)
  - [auth check](#auth-check)
  - [auth login](#auth-login)
  - [auth logout](#auth-logout)
- [bookmark](#bookmark)
  - [bookmark add](#bookmark-add)
  - [bookmark delete](#bookmark-delete)
  - [bookmark edit](#bookmark-edit)
  - [bookmark get](#bookmark-get)
  - [bookmark list](#bookmark-list)
- [channel](#channel)
  - [channel archive](#channel-archive)
  - [channel create](#channel-create)
  - [channel edit](#channel-edit)
  - [channel get](#channel-get)
  - [channel list](#channel-list)
  - [channel read](#channel-read)
  - [channel unarchive](#channel-unarchive)
- [file](#file)
  - [file download](#file-download)
  - [file list](#file-list)
  - [file remove](#file-remove)
  - [file replace](#file-replace)
- [pin](#pin)
  - [pin add](#pin-add)
  - [pin list](#pin-list)
  - [pin remove](#pin-remove)
- [post](#post)
  - [post create](#post-create)
  - [post delete](#post-delete)
  - [post edit](#post-edit)
  - [post get](#post-get)
  - [post history](#post-history)
  - [post list](#post-list)
  - [post restore](#post-restore)
  - [post search](#post-search)
  - [post thread](#post-thread)
- [profile](#profile)
  - [profile default](#profile-default)
  - [profile list](#profile-list)
  - [profile remove](#profile-remove)
- [saved](#saved)
  - [saved add](#saved-add)
  - [saved list](#saved-list)
  - [saved remove](#saved-remove)
- [thread](#thread)
  - [thread follow](#thread-follow)
  - [thread list](#thread-list)
  - [thread read](#thread-read)
  - [thread unfollow](#thread-unfollow)
- [messages](#messages)
- [send](#send)
- [watch](#watch)

## Global flags

```
Flags:
  -h, --help             help for mm-cli
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)
  -V, --version          print the version

Use "mm-cli [command] --help" for more information about a command.
```

## auth

```
Log in, check and end sessions

Usage:
  mm-cli auth [command]

Available Commands:
  check       Show who is logged in and the server version
  login       Log in through the browser (SSO) or with a token from stdin
  logout      End the session on the server and delete the local token

Flags:
  -h, --help   help for auth

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli auth [command] --help" for more information about a command.
```

### auth check

```
Show who is logged in and the server version

Usage:
  mm-cli auth check [flags]

Flags:
  -h, --help   help for check

```

### auth login

```
Log in through the same browser flow the desktop app uses.

mm-cli opens the sign-in page; after signing in, the browser lands on a
.../login/desktop?... page. Paste that address into the terminal. Without a
terminal, mm-cli prints the sign-in address and exits with code 0 without
logging in (with --json it prints {"status": "pending", ...}); finish the
login within 3 minutes with --callback - and the address on stdin.
--callback '<address>' also works, but the address carries a one-time token
and is then visible in the process list and the shell history.

--token-stdin reads a personal access token or a session token from stdin.
Tokens are never accepted as arguments: those show up in the process list.

The profile name defaults to the server host with dots replaced by '-'.

Usage:
  mm-cli auth login [<url>] [flags]

Examples:
  mm-cli auth login https://chat.example.com
  mm-cli auth login https://chat.example.com --no-browser
  mm-cli auth login --profile chat-example-com --callback - < address.txt
  mm-cli auth login https://chat.example.com --token-stdin < token.txt

Flags:
      --callback string   finish a started login with the /login/desktop address; '-' reads it from stdin (an address given here is visible in the process list)
  -h, --help              help for login
      --no-browser        print the sign-in address without opening a browser
      --sso string        SSO method when the server has several: saml, gitlab, openid, google, office365
      --token-stdin       read the token from stdin

```

### auth logout

```
End the session on the server and delete the local token

Usage:
  mm-cli auth logout [flags]

Flags:
  -h, --help   help for logout

```

## bookmark

```
Channel bookmarks (links and files under the channel header)

Usage:
  mm-cli bookmark [command]

Available Commands:
  add         Add a link or file bookmark to a channel
  delete      Delete a bookmark
  edit        Change a bookmark
  get         Show one bookmark
  list        List the bookmarks of a channel

Flags:
  -h, --help   help for bookmark

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli bookmark [command] --help" for more information about a command.
```

### bookmark add

```
Add a link or file bookmark to a channel

Usage:
  mm-cli bookmark add <channel> (--url <url> | -f <file>) --name <name> [--emoji <name>] [flags]

Flags:
      --emoji string   emoji name, without colons
  -f, --file string    file to upload as the bookmark
  -h, --help           help for add
      --name string    bookmark title
      --url string     link target

```

### bookmark delete

```
Delete a bookmark

Usage:
  mm-cli bookmark delete <channel> <bookmark-id> [flags]

Flags:
  -h, --help   help for delete
  -y, --yes    do not ask for confirmation

```

### bookmark edit

```
Change a bookmark

Usage:
  mm-cli bookmark edit <channel> <bookmark-id> [--name <name>] [--url <url>] [--emoji <name>] [flags]

Flags:
      --emoji string   new emoji name
  -h, --help           help for edit
      --name string    new title
      --url string     new link target

```

### bookmark get

```
Show one bookmark

Usage:
  mm-cli bookmark get <channel> <bookmark-id> [flags]

Flags:
  -h, --help   help for get

```

### bookmark list

```
List the bookmarks of a channel

Usage:
  mm-cli bookmark list <channel> [flags]

Flags:
  -h, --help   help for list

```

## channel

```
Channels

Usage:
  mm-cli channel [command]

Available Commands:
  archive     Archive a channel (reversible with unarchive)
  create      Create a channel in the team; you become a member
  edit        Change the display name, purpose or header
  get         Show a channel: type, names, header, purpose, members, messages
  list        List your channels
  read        Mark a channel as read
  unarchive   Restore an archived channel

Flags:
  -h, --help   help for channel

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli channel [command] --help" for more information about a command.
```

### channel archive

```
Archive a channel (reversible with unarchive)

Usage:
  mm-cli channel archive <channel> [flags]

Flags:
  -h, --help   help for archive

```

### channel create

```
Create a channel in the team; you become a member

Usage:
  mm-cli channel create <name> [flags]

Examples:
  mm-cli channel create notes --private
  mm-cli channel create notes --private --exist-ok --team myteam

Flags:
      --display-name string   display name (default: the name)
      --exist-ok              succeed without changes if the channel already exists
      --header string         channel header
  -h, --help                  help for create
      --private               create a private channel
      --purpose string        channel purpose

```

### channel edit

```
Change the display name, purpose or header

Usage:
  mm-cli channel edit <channel> [--display-name <name>] [--purpose <text>] [--header <text>] [flags]

Flags:
      --display-name string   new display name
      --header string         new header
  -h, --help                  help for edit
      --purpose string        new purpose

```

### channel get

```
The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli channel get <channel> [flags]

Flags:
  -h, --help   help for get

```

### channel list

```
List your channels in the selected team (--team or the profile's team), or
in all your teams when none is selected. --type takes a comma-separated set of
O (open), P (private), D (direct), G (group).

Usage:
  mm-cli channel list [flags]

Flags:
  -h, --help          help for list
      --type string   channel types: O,P,D,G
      --unread        only channels with unread messages or mentions

```

### channel read

```
Mark a channel as read. Reading messages with mm-cli never does this by itself.

Usage:
  mm-cli channel read <channel> [flags]

Flags:
  -h, --help   help for read

```

### channel unarchive

```
Restore an archived channel

Usage:
  mm-cli channel unarchive <channel> [flags]

Flags:
  -h, --help   help for unarchive

```

## file

```
Attachments: list, download, replace, remove

Usage:
  mm-cli file [command]

Available Commands:
  download    Download attachments of messages, or files by id
  list        List recent attachments of a channel, newest first
  remove      Remove one attachment from your message
  replace     Replace one attachment of your message

Flags:
  -h, --help   help for file

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli file [command] --help" for more information about a command.
```

### file download

```
Download attachments. An id is tried as a message first, then as a file;
--post or --file takes every id as that type and saves the other request.
Attachments with the same name are saved as "name (2).ext", "name (3).ext" in
the order of the arguments. Existing files are kept unless --force is given; all
conflicts are reported before anything is downloaded.

Usage:
  mm-cli file download <post-or-file-id>... [flags]

Flags:
      --file            the ids are file ids
      --force           overwrite existing files
  -h, --help            help for download
  -o, --output string   directory to save into (default ".")
      --post            the ids are message ids

```

### file list

```
List recent attachments of a channel, newest first. Looks through at most the
last 1000 messages.

The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli file list <channel> [flags]

Flags:
  -h, --help        help for list
  -n, --limit int   number of files (1-200) (default 20)

```

### file remove

```
Remove one attachment from your message

Usage:
  mm-cli file remove <post-id> <file-id> [flags]

Flags:
  -h, --help   help for remove
  -y, --yes    do not ask for confirmation

```

### file replace

```
Replace one attachment of your message

Usage:
  mm-cli file replace <post-id> <file-id> <new-file> [flags]

Flags:
  -h, --help   help for replace

```

## pin

```
Pinned messages of a channel

Usage:
  mm-cli pin [command]

Available Commands:
  add         Pin a message to its channel
  list        Show pinned messages, oldest first
  remove      Unpin a message

Flags:
  -h, --help   help for pin

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli pin [command] --help" for more information about a command.
```

### pin add

```
Pin a message to its channel

Usage:
  mm-cli pin add <post-id> [flags]

Flags:
  -h, --help   help for add

```

### pin list

```
The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli pin list <channel> [flags]

Flags:
  -h, --help           help for list
      --truncate int   shorten messages to this many characters

```

### pin remove

```
Unpin a message

Usage:
  mm-cli pin remove <post-id> [flags]

Flags:
  -h, --help   help for remove

```

## post

```
Create, read, edit and delete messages

Usage:
  mm-cli post [command]

Available Commands:
  create      Send a message with optional attachments
  delete      Delete messages, one request at a time
  edit        Replace the text of your message
  get         Show one message
  history     Show earlier versions of your message
  list        Show recent messages, oldest first
  restore     Restore an earlier version of your message
  search      Search messages in the team
  thread      Show a whole thread, oldest first

Flags:
  -h, --help   help for post

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli post [command] --help" for more information about a command.
```

### post create

```
Send a message. The text is an argument or '-' for stdin.

The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli post create <channel> [<text> | -] [-f <file>]... [flags]

Examples:
  mm-cli send @me "note to self"
  mm-cli send @me -f report.pdf -f photo.jpg
  echo "multi-line text" | mm-cli send myteam/town-square -
  mm-cli send town-square "reply" --reply <post-id>

Flags:
  -f, --file stringArray   attach a file (repeatable, up to 10)
  -h, --help               help for create
      --reply string       reply in the thread of this post id

```

### post delete

```
Delete messages, one request at a time

Usage:
  mm-cli post delete <post-id>... [flags]

Flags:
  -h, --help   help for delete
  -y, --yes    do not ask for confirmation

```

### post edit

```
Replace the text of your message

Usage:
  mm-cli post edit <post-id> [<text> | -] [flags]

Flags:
  -h, --help   help for edit

```

### post get

```
Show one message

Usage:
  mm-cli post get <post-id> [flags]

Flags:
  -h, --help   help for get

```

### post history

```
Show earlier versions of your edited message, oldest first. The id of each version is what 'post restore' takes.

Usage:
  mm-cli post history <post-id> [flags]

Flags:
  -h, --help   help for history

```

### post list

```
Show the most recent messages of a channel, oldest first. Reading does not
mark the channel as read.

--since keeps only messages after a time (RFC 3339, or 90m, 2h, 3d back from
now); --before shows messages older than a post. Both still show at most -n.

The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli post list <channel> [flags]

Examples:
  mm-cli messages @me
  mm-cli post list town-square -n 50 --since 3d
  mm-cli post list myteam/town-square --json

Flags:
      --before string   only messages older than this post id
  -h, --help            help for list
  -n, --limit int       number of messages (1-200) (default 20)
      --since string    only messages after this time
      --truncate int    shorten messages to this many characters

```

### post restore

```
Restore an earlier version of your message

Usage:
  mm-cli post restore <post-id> <version-id> [flags]

Flags:
  -h, --help   help for restore

```

### post search

```
Search messages in the selected team with the Mattermost search syntax
("exact phrase", -excluded, in:channel, from:user, before:/after:/on:YYYY-MM-DD).
--in and --from add in: and from: to the query.

Usage:
  mm-cli post search <query> [flags]

Examples:
  mm-cli post search "release notes" -n 10
  mm-cli post search deploy --in town-square --from @bob

Flags:
      --from string    only from this user (@name)
  -h, --help           help for search
      --in string      only in this channel (name or team/name)
  -n, --limit int      number of results (1-200) (default 20)
      --truncate int   shorten messages to this many characters

```

### post thread

```
Show a whole thread, oldest first

Usage:
  mm-cli post thread <post-id> [flags]

Flags:
  -h, --help           help for thread
      --truncate int   shorten messages to this many characters

```

## profile

```
List, choose and remove profiles

Usage:
  mm-cli profile [command]

Available Commands:
  default     Show or set the default profile
  list        List profiles; the default one is marked with *
  remove      Remove a profile, its local session and cache

Flags:
  -h, --help   help for profile

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli profile [command] --help" for more information about a command.
```

### profile default

```
Show or set the default profile

Usage:
  mm-cli profile default [<name>] [flags]

Flags:
  -h, --help   help for default

```

### profile list

```
List profiles; the default one is marked with *

Usage:
  mm-cli profile list [flags]

Flags:
  -h, --help   help for list

```

### profile remove

```
Remove a profile with its local session file and cache. The session on the
server stays valid until it expires; run 'mm-cli auth logout' first to end it.

Usage:
  mm-cli profile remove <name> [flags]

Flags:
  -h, --help   help for remove
  -y, --yes    do not ask for confirmation

```

## saved

```
Saved messages (visible only to you)

Usage:
  mm-cli saved [command]

Available Commands:
  add         Save a message
  list        Show saved messages, oldest first
  remove      Remove a message from saved

Flags:
  -h, --help   help for saved

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli saved [command] --help" for more information about a command.
```

### saved add

```
Save a message

Usage:
  mm-cli saved add <post-id> [flags]

Flags:
  -h, --help   help for add

```

### saved list

```
Show saved messages, oldest first

Usage:
  mm-cli saved list [flags]

Flags:
  -h, --help           help for list
  -n, --limit int      number of messages (1-200) (default 20)
      --truncate int   shorten messages to this many characters

```

### saved remove

```
Remove a message from saved

Usage:
  mm-cli saved remove <post-id> [flags]

Flags:
  -h, --help   help for remove

```

## thread

```
Threads are the replies under a root post ("Threads" in the Mattermost
web app, Collapsed Reply Threads). A thread is identified by the id of its
root post: the same id that "mm-cli post thread <id>" reads and
"mm-cli send <channel> <text> --reply <id>" replies to. Giving the id of a
reply works too: the command acts on its root post.

You follow the threads you started or replied to, and any thread you follow
by hand. Reading (thread list, post thread, watch) does not mark anything as
read; only "mm-cli thread read" does.

Threads are kept per team: --team, or the profile's team, selects one;
without either, thread list and thread read --all cover all your teams.

Usage:
  mm-cli thread [command]

Examples:
  mm-cli thread list --unread
  mm-cli post thread <root-post-id>
  mm-cli send town-square "my answer" --reply <root-post-id>
  mm-cli thread read <root-post-id>

Available Commands:
  follow      Follow threads
  list        Show followed threads, most recent reply first
  read        Mark threads as read
  unfollow    Stop following threads

Flags:
  -h, --help   help for thread

Global Flags:
      --json             print JSON
      --profile string   profile name (default: $MM_PROFILE or the default profile)
      --team string      team name (default: the profile's team)

Use "mm-cli thread [command] --help" for more information about a command.
```

### thread follow

```
Follow threads, so that new replies show up in thread list.
Following also marks the thread read: the server counts only replies that
come after it as unread. Each argument is a
post id: a root post, or a reply (the command acts on its root post).

With --json the output is an array of {"action": "followed", "id": <root post id>}.
Lines are printed as each thread is done, so after an error the earlier
threads are already changed.

Exit code 2 for an invalid id, 4 without access to the post, 5 when the
post does not exist.

Usage:
  mm-cli thread follow <post-id>... [flags]

Examples:
  mm-cli thread follow <post-id>
  mm-cli thread follow <post-id> <post-id> --json

Flags:
  -h, --help   help for follow

```

### thread list

```
Show the threads you follow, the one with the most recent reply first: time
of the last reply, channel, author and text of the root post, number of
replies, unread replies and unread mentions, and the root post id.

--unread keeps only threads with unread replies. Listing does not mark
anything as read; read the replies with "mm-cli post thread <id>".

With --json the output is an array of objects with the fields id, team,
channel_id, channel (an address mm-cli accepts: team/name, @user, or an id
for group channels), channel_type (O, P, D, G), root (the root post, as in
post list), reply_count, unread_replies, unread_mentions and last_reply_at
(RFC 3339, empty when there are no replies).

Usage:
  mm-cli thread list [flags]

Examples:
  mm-cli thread list --unread
  mm-cli thread list -n 50 --truncate 80
  mm-cli thread list --unread --json

Flags:
  -h, --help           help for list
  -n, --limit int      number of threads (1-200) (default 20)
      --truncate int   shorten messages to this many characters
      --unread         only threads with unread replies

```

### thread read

```
Mark threads as read, up to now. Each argument is a post id: a root post, or
a reply (its thread is marked). --all marks every followed thread as read in
the selected team (--team or the profile's team), or in each of your teams.

With --json the output is an array of {"action": "marked_read", "id": ...},
or {"action": "marked_read", "team": ...} for --all.

Exit code 2 when neither ids nor --all are given, or both are; 4 without
access to the post; 5 when the post does not exist.

Usage:
  mm-cli thread read (<post-id>... | --all) [flags]

Examples:
  mm-cli thread read <post-id>
  mm-cli thread read --all
  mm-cli thread read --all --team myteam --json

Flags:
      --all    mark every followed thread as read (in --team, or in all your teams)
  -h, --help   help for read

```

### thread unfollow

```
Stop following threads; they disappear from thread list. Each argument is a
post id: a root post, or a reply (the command acts on its root post).

With --json the output is an array of {"action": "unfollowed", "id": <root post id>}.
Lines are printed as each thread is done, so after an error the earlier
threads are already changed.

Exit code 2 for an invalid id, 4 without access to the post, 5 when the
post does not exist.

Usage:
  mm-cli thread unfollow <post-id>... [flags]

Examples:
  mm-cli thread unfollow <post-id>
  mm-cli thread unfollow <post-id> <post-id> --json

Flags:
  -h, --help   help for unfollow

```

## messages

```
Show the most recent messages of a channel, oldest first. Reading does not
mark the channel as read.

--since keeps only messages after a time (RFC 3339, or 90m, 2h, 3d back from
now); --before shows messages older than a post. Both still show at most -n.

The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli messages <channel> [flags]

Examples:
  mm-cli messages @me
  mm-cli post list town-square -n 50 --since 3d
  mm-cli post list myteam/town-square --json

Flags:
      --before string   only messages older than this post id
  -h, --help            help for messages
  -n, --limit int       number of messages (1-200) (default 20)
      --since string    only messages after this time
      --truncate int    shorten messages to this many characters

```

## send

```
Send a message. The text is an argument or '-' for stdin.

The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli send <channel> [<text> | -] [-f <file>]... [flags]

Examples:
  mm-cli send @me "note to self"
  mm-cli send @me -f report.pdf -f photo.jpg
  echo "multi-line text" | mm-cli send myteam/town-square -
  mm-cli send town-square "reply" --reply <post-id>

Flags:
  -f, --file stringArray   attach a file (repeatable, up to 10)
  -h, --help               help for send
      --reply string       reply in the thread of this post id

```

## watch

```
Print new messages as they arrive, until interrupted. Without arguments, all
channels you are a member of are watched.

Watching keeps one WebSocket connection open; there is no polling. After the
connection drops it is opened again, waiting 1, 2, 4 ... up to 60 seconds, or
as long as the server asks when it answers 429 Too Many Requests.
Watching does not mark channels as read. With --json, each message is one
JSON object per line.

The channel is @me (your direct channel with yourself), @user (a direct
channel, created on first use), a channel name in the default team,
team/channel, or a 26-character channel id.

Usage:
  mm-cli watch [<channel>...] [flags]

Flags:
  -h, --help           help for watch
      --truncate int   shorten messages to this many characters

```
