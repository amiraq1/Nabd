# Security policy

## Supported versions

Security fixes land on `master` and are cut in the next tagged release.
Only the latest tagged release is supported. Older tags, including `v1.2.0`
and earlier, receive no backports.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/amiraq1/Nabd/security/advisories/new)
on this repository. Do not open a public issue for a suspected escape.

We aim to acknowledge a report within 7 days and to ship a fix, or a
reasoned decline, within 90 days of a complete report. Public disclosure
before that window needs agreement from both sides unless a fix is already
released.

## What is a vulnerability

**Is:** a tool that reads or writes a path `Root.Resolve` would refuse, without
a permission prompt. A standing grant that applies to `bash`. A bash child
that inherits a provider key, `BASH_ENV`, `ENV`, or the caller's `HOME`.
A config file with mode wider than `0600`, a symlink, or a non-regular file
that is still loaded.

**Is not:** the model ran a destructive command the operator approved at the
prompt. `cd ..` or `rm -rf ~/x` from an approved `bash` call. Damage inside
the project root after `write_file` / `edit_file` was allowed. A local
attacker who already shares the operator's uid.

The full containment story, with test names, is `docs/THREAT_MODEL.md`.
