# TODO

Known gaps that were left out on purpose, with the reason.

## Contents

- [Release artifacts](#release-artifacts)
- [Channel addresses](#channel-addresses)
- [Terminal output](#terminal-output)

## Release artifacts

- Sign the binaries and notarize the macOS builds. Until then macOS users remove the quarantine attribute by hand (see the README).
- Attach an SBOM to each release. goreleaser can produce one, but it needs an extra tool (syft) in the release job.

## Channel addresses

- A channel name of 26 lowercase letters and digits is read as a channel id, since ids look the same. When such an id is not found, mm-cli could retry it as a name. This needs a change in the retry logic of `withChannel` and `checkedChannel`; until then such a channel is addressed by its id.

## Terminal output

- Text output replaces control characters from the server but keeps Unicode bidirectional controls (U+202A–U+202E, U+2066–U+2069), which can reorder how a line is displayed. Decide whether to replace them as well.
