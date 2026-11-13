# 0026. The secret-store account names the server and its address

- Status: Accepted
- Date: 2026-11-13

## Context

ADR 0025 keeps the session token in the OS secret store under service `mm-cli` and the saved server's name as the account. The name is only a label in `config.yaml`, and the store is shared by every configuration directory of the user (`$MM_CONFIG_DIR` differs per profile). Two profiles that both call a server `chat`, one pointing at `https://chat.example.com` and the other at `https://other.example.com`, therefore shared and overwrote one entry, and a token issued by one server could be sent to the other address.

No release with ADR 0025 has shipped, so no stored entry has to be carried over.

## Decision

- The account is the saved name and the server's address, separated by one space: `chat https://chat.example.com`. The service stays `mm-cli`. The pair stays readable in the keyring's own UI, and a token is found only for the address it was issued for.
- The address comes from `config.yaml`; while a login is in progress and the server is not saved yet, from the pending login (`sessions/<name>.pending`). Saving a token for a server with no known address fails.
- Reading a token for an unknown address finds nothing (exit code 3 as for any missing session). Deleting a token whose server is no longer in `config.yaml` removes nothing, because the entry cannot be named any more; commands that delete a server remove its token first.
- The same server saved under the same name and address in two profiles shares one entry, which is intended: both profiles hold the same session.
- Files (`token_storage: file`) are unchanged: `sessions/<name>.token` lives inside the configuration directory, so it cannot be shared between profiles.
- The file-to-secret-store migration of ADR 0025 is unchanged; it writes under the new account.

## Consequences

- Same name with different addresses never collide, whether in one profile after an address change or across profiles.
- Changing a saved server's address makes its old token unreachable: log in again. The orphaned entry can be removed by hand in the keyring's UI (it is listed as `<name> <old address>`).
- Account names are longer and contain the address; tokens are still the only secrets in the entries.
- ADR 0025 is superseded as to the account name only; everything else in it stands.
