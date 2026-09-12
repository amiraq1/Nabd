from pathlib import Path
import re

p = Path('README.md')
s = p.read_text()
pat = r'\*\*No GitHub Release has been published yet:\*\*.*?See `docs/RELEASING\.md`\.\n'
rep = '''Published releases are available on the [Releases page](https://github.com/amiraq1/Nabd/releases):

| Release | Published | Assets |
|---|---|---|
| `v1.4.0` | 2026-09-11 | Platform binaries and `checksums.txt` |
| `v1.3.0` | 2026-09-11 | Platform binaries and `checksums.txt` |

Verify a downloaded release from its directory:

```sh
sha256sum -c checksums.txt
```

See `docs/RELEASING.md` for the release process.
'''
s, n = re.subn(pat, rep, s, count=1, flags=re.S)
assert n == 1, 'README release block not found'
p.write_text(s)

p = Path('SECURITY.md')
s = p.read_text()
old = 'Only the latest tagged release is supported. Older tags, including `v1.2.0`\nand earlier, receive no backports.\n'
new = 'Only the latest tagged release is supported. Older tagged releases receive no\nbackports.\n'
assert old in s, 'SECURITY version block not found'
p.write_text(s.replace(old, new, 1))

p = Path('docs/THREAT_MODEL.md')
s = p.read_text()
pat = r'Sourced from the code as of commit d63da42.*?code\), not from intention\.\n'
rep = ('Sourced from the code at `master` commit '
       '`7d9e2f07f9561d4e79b2db6164387b3eac6660ff`, not from intention.\n'
       'Last reviewed: 2026-09-12 · `7d9e2f07f9561d4e79b2db6164387b3eac6660ff`.\n')
s, n = re.subn(pat, rep, s, count=1, flags=re.S)
assert n == 1, 'THREAT_MODEL provenance block not found'
p.write_text(s)

p = Path('.github/workflows/ci.yml')
s = p.read_text()
anchor = "    - name: Environment diagnostics\n      run: go env GOOS GOARCH CGO_ENABLED CC\n"
step = '''
    - name: Threat model freshness
      if: github.event_name == 'pull_request'
      run: |
        base=${{ github.event.pull_request.base.sha }}
        sec=$(git diff --name-only "$base" HEAD -- \\
          internal/tools/path.go internal/tools/bash.go internal/perm/policy.go \\
          internal/config/config.go internal/snap/shadow.go internal/agent/fence.go \\
          cmd/ag/main.go)
        doc=$(git diff --name-only "$base" HEAD -- docs/THREAT_MODEL.md)
        if [ -n "$sec" ] && [ -z "$doc" ]; then
          echo "security-relevant files changed without THREAT_MODEL.md update:"
          echo "$sec"
          exit 1
        fi
'''
assert anchor in s, 'CI insertion anchor not found'
p.write_text(s.replace(anchor, anchor + step, 1))
