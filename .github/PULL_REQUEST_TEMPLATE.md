## Summary

Describe the change and its user-visible effect.

## Validation

List commands or CI jobs that verify the change.

## Required security checklist

- [ ] I classified whether this changes paths, permissions, shell execution, configuration, credentials, journal/shadow storage, or release artifacts.
- [ ] I updated `docs/THREAT_MODEL.md` when a security-relevant contract changed, or confirmed that no claim changed.
- [ ] I added or updated a regression test for every changed guarantee, or explained why no test is applicable.
- [ ] I checked that logs, fixtures, and diffs contain no credentials or raw sensitive journals.
- [ ] I verified third-party actions are pinned to full commit SHAs.
