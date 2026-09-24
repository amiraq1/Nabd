## Summary

Describe the change and its user-visible effect.

## Validation

List commands or CI jobs that verify the change.

## Required security checklist

Tick every item. An item that genuinely does not apply may instead be marked
`- [x] N/A: <reason>` on its own line, with a reason of at least 10 characters.
The items marked **(always applies)** state an invariant that every pull request
must satisfy; they can never be marked N/A.

- [ ] I classified whether this changes paths, permissions, shell execution, configuration, credentials, journal/shadow storage, or release artifacts. **(always applies)**
- [ ] I updated `docs/THREAT_MODEL.md` when a security-relevant contract changed, or confirmed that no claim changed.
- [ ] I added or updated a regression test for every changed guarantee, or explained why no test is applicable.
- [ ] I checked that logs, fixtures, and diffs contain no credentials or raw sensitive journals. **(always applies)**
- [ ] I verified third-party actions are pinned to full commit SHAs. **(always applies)**
