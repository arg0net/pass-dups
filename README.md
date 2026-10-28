# pass-dups

A Go / Bubble Tea TUI for reviewing duplicate Proton Pass logins. It finds entries
linked by the same registrable domain, lets you compare and merge their fields,
and stages decisions for a single batch approval.

## Build and try it offline

Requires Go 1.27 or newer.

```sh
go build -o pass-dups .
./pass-dups --demo
```

The demo contains **synthetic credentials only**. It uses an in-memory replacement
for `pass-cli`, including simulated updates and trash operations. It never starts
`pass-cli`, requires no account, and resets when restarted.

## Use with Proton Pass

Install Proton's `pass-cli` and authenticate separately with `pass-cli login`, then
run:

```sh
./pass-dups
```

Use `--pass-cli /path/to/pass-cli` if the executable is outside `PATH`. The adapter
targets the `pass-cli` 2.6.0 JSON interfaces. The application uses the CLI's existing
session and permissions; it does not manage authentication.

1. Select a vault. The default scanner uses only names from the login item
   list; it does not read full item details or secret fields. Counts show how many
   logins have recognized domains. Press **d** to inspect each name and the domains
   detected from it. Both Active and Trashed logins are included, and each group
   shows its state counts. A failed scan is shown separately from an empty result.
2. Open a collision group. Only that group's full details are fetched, then kept
   in memory for review. Select an existing merge target with left/right.
   Entries show their state, creation time and last-modified time. Changing the
   target resets the draft's choices and keeps other Active entries; already
   Trashed entries stay unchanged.
3. Choose entries to keep or trash. All entries initially remain kept. Choose a
   source entry for each scalar field, and toggle the desired URL and note blocks.
   To discard the entire group, press **d**, or cycle the target row to **Keep none**.
   This selects every Active entry for Trash and performs no field updates.
4. Press **s** to preview the result, then **Enter** to stage it. Review more groups.
5. Press **b** from the group list to review the entire batch. **a** opens final
   approval; **y** applies it. No updates or trash operations run before approval.

The selection list marks groups as **Pending** or **Resolved** and shows counts of
each. A Resolved group has a staged decision awaiting batch approval; Keep none
decisions have their own label. Unstaging a decision marks its group Pending again.
After staging, the selection stays on the group you just resolved.

Multiple entries can remain in a group. A domain match is a review candidate:
personal and work accounts on the same site often need to stay separate.

**Keep none** uses the same preview, staging and final batch approval as other
decisions. Already-Trashed entries stay unchanged, and extra-data acknowledgment
appears only when an Active entry selected for Trash contains nonempty additional
data. Empty containers and extras on retained or already-Trashed entries do not
require acknowledgment. Choose an Active
target again to leave Keep none mode and reset the draft's choices.

To include saved website URLs as well as item names, explicitly opt in:

```sh
./pass-dups --all-urls
```

This mode reads every Active and Trashed login's full details during scanning because item-list
metadata does not include its saved URLs. Both modes re-fetch affected entries
before applying changes. Use `./pass-dups --demo --all-urls` to try the broader scan
with synthetic data.

## Keys

| Screen | Keys |
| --- | --- |
| Everywhere | `q` quit; arrows or `j`/`k` navigate; `PgUp`/`PgDn` scroll |
| Vaults | `Enter` select; `r` retry listing |
| Groups | `Enter` load/review selected group; `/` filter; `Esc` clear filter; `d` detected domains; `b` batch; `x` unstage; `r` rescan; `v` switch vault |
| Detected domains | arrows or `PgUp`/`PgDn` scroll; `Esc` back; this view performs no additional reads |
| Comparison | left/right or `h`/`l` choose target/source, including Keep none on the target row; `Space` toggle; `d` keep none (trash entire group); `r` reveal secrets; `s` preview; `x` unstage/reset; `[`/`]` previous/next group; `n` skip to next; `Esc` back |
| Preview | `Enter` stage; `Esc` revise |
| Staged batch | `Enter` revise selected decision; `x` unstage; `a` request approval; `Esc` back |
| Approval | `y` confirm; `n` or `Esc` cancel |
| Running operation | `Esc` or `Ctrl+C` cancel and wait for its result |
| Results | `r` or `Enter` rescan; `q` quit |

Passwords and extra data are masked initially; equality and difference indicators
work while masked. Revealing values applies only to the current comparison and
resets when leaving it. Terminal control and bidirectional formatting characters
in stored data are replaced for display; original field values remain intact.

## Matching and merge behavior

- Domains and HTTP(S) URLs anywhere in an item name contribute to default matching,
  including quoted/bracketed URLs, names with copy-number suffixes, and names with
  punctuation beside the hostname. Email addresses are not treated as website
  domains. `user.wolfram.com` and `wolfram.com` group together as `wolfram.com`.
- Saved website URLs and advanced autofill URLs contribute only in `--all-urls`
  mode. Opening a default-scan group loads its details without expanding discovery
  to entries in other groups or entries whose names have no recognized domain.
- Names are normalized with IDNA and the Public Suffix List, including private
  suffixes. `shop.example.co.uk` matches `login.example.co.uk`; `alice.github.io`
  stays separate from `bob.github.io`. IP addresses, local hosts and malformed
  domains do not contribute matches.
- An entry referencing several domains connects the matching entries into one
  group. The comparison displays the domain links and member IDs.
- Active entries precede Trashed entries. Groups with multiple Active entries
  come first, groups with one Active entry follow, and Trashed-only groups come
  last. The default target and scalar sources are Active; Trashed URLs and notes
  are not selected automatically. You may explicitly choose Trashed data to copy
  into an Active target. Trashed entries cannot be targets or be trashed again;
  Trashed-only groups support comparison without staging changes.
- Creation and last-modified timestamps retain the precision and timezone supplied
  by the CLI. Timestamps without a timezone remain without one. Missing values
  display as `unknown`. Dates and states also appear in batch previews.
- Alias and other non-login records are excluded from password merge groups.
- Standard merge fields are title, username, email, password, legacy URLs and
  notes. Scalar values come from a chosen source entry. Selected URLs retain
  their displayed order; selected distinct note blocks join with a blank line.
- Custom fields, TOTP seeds, passkeys, attachments, advanced autofill rules and
  unknown extra data are comparison-only. They remain on the target. Trashing an
  entry containing extras requires explicit acknowledgment that they will not be
  transferred. Keep that entry if its additional data is still needed.
- The CLI represents URL updates as a comma-separated string. The app rejects
  changes that would alter a URL containing a literal comma or surrounding
  whitespace. Existing such URLs can remain unchanged. Targets with advanced
  autofill rules cannot have their legacy URLs changed through this tool.
- The app rejects standard field changes shadowed by a same-named custom field,
  because the CLI would otherwise update the custom field instead.

## Applying and recovering

The app checks all reviewed entries before starting a batch, checks each group
again before processing it, and checks each source immediately before trashing.
It updates the target first and verifies its fields, additional data and metadata
before trashing any source. It uses share and item IDs, retaining the target's ID.

Batches are **not atomic**: Proton's CLI does not expose transactions or conditional
writes. An external change can still race a check. Avoid editing affected entries
in another client while applying a batch.

Any failure or cancellation stops further operations. Results distinguish
completed, failed/uncertain and unattempted operations. An interrupted command may
already have changed the vault. After any batch attempt, staged decisions are
cleared and the app requires a fresh scan before further cleanup. It never blindly
retries writes or automatically rolls back a partly applied batch.

Cleanup moves entries to **Trash**. Restore them through Proton Pass if needed.
The application does not permanently delete entries or manage vault membership.

Staged decisions and fetched details are held in memory only. Quitting, rescanning
or switching vaults discards them, with confirmation when decisions are pending.
There is no on-disk history, export, credential cache or resumable session in this
application. Go's managed memory does not guarantee immediate credential erasure.

The app does not log credentials, raw CLI responses or command arguments. The CLI's
update interface requires field values in subprocess arguments, so changed
passwords can be temporarily visible to local process inspection. CLI stdout and
stderr are captured; displayed errors are sanitized.

## Development and tests

**Do not retrieve real secrets or modify real vaults during development.** Do not
run secret-reading `pass-cli` commands or connect development builds to a live
account. CLI help/version inspection is permitted. Use `--demo` and the synthetic
fixtures under `internal/demo/testdata` for all development and verification.
The metadata fixture in `internal/passcli/testdata/item-list.json` mirrors the
reported flat `items` array, including `vault_id`, `flags`, ISO timestamp strings,
mixed item types/states and optional `folder_id`. IDs and titles are synthetic.
The demo builds summaries in that same form; its full-detail responses use the
separate `item` / `content` structure returned by `item view`.

```sh
go test ./...
go test -race ./...
go vet ./...
go build -o pass-dups .
```

Tests inject a fake command runner. Subprocess error/cancellation tests invoke the
Go test executable itself, never `pass-cli`. Coverage includes domain matching,
transitive groups, JSON validation, merge selection, extra-data acknowledgment,
stale entries, partial failures, cancellation, secret masking, responsive views,
an end-to-end offline TUI batch, metadata-only scanning, per-group lazy loading,
scan-error presentation, and the reported Wolfram hostname pair.
Mixed-state tests cover Active source defaults, Trashed-only comparisons, timestamp
preservation, and leaving Trashed sources unchanged during an explicit merge.

The implementation is split into `internal/core` (matching, merge plans and batch
execution), `internal/passcli` (JSON/process boundary), `internal/tui` (Bubble Tea),
and `internal/demo` (synthetic in-memory CLI).

Protocol references: [Proton CLI commands](https://protonpass.github.io/pass-cli/commands/item/),
[Proton's public data types](https://github.com/protonpass/proton-pass-common),
and [Bubble Tea](https://github.com/charmbracelet/bubbletea).
