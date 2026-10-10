# Termux Arabic shaping measurements

## Purpose

`docs/rtl-feed-integration.md` states that Arabic shaping is deliberately
disabled, and that "a later change may add an explicit shaping policy after
Termux behavior is measured". This document records those measurements.

The probe (`tools/termux-shaping-probe/probe.py`) is measurement-only: it
imports nothing from the project, performs no shaping, and does not read or
set `NABD_RTL`. It prints the environment and a fixed matrix of cases; the
human observer judges rendering from the screen.

## Environment

| Field | Value |
|---|---|
| Date | 2026-10-10 |
| Termux version | 0.119.0-beta.3 (F-Droid) |
| termux-tools | 1.45.0 |
| Android | 16 |
| Kernel | 5.15.180-android13-8 |
| Device | Xiaomi 23078PND5G (aarch64) |
| Python | 3.14.6 |
| TERM | xterm-256color |
| LANG | en_US.UTF-8 |
| LC_ALL | (unset) |
| Font | Termux default (see screenshots) |

## Questions

Three independent questions, answered separately:

- **Q1**: Does Termux join logical Arabic letters automatically?
- **Q2**: Do Arabic presentation forms (U+FE70–U+FEFF) render correctly?
- **Q3**: Do presentation forms and logical letters occupy equal column widths?

## Case matrix

| ID | Content | Codepoints | Purpose |
|---|---|---|---|
| L1 | مرحبا | U+0645 U+0631 U+062D U+0628 U+0627 | Q1 |
| L2 | بب | U+0628 U+0628 | Q1 (joining identical letters) |
| P1 | ﺏ ﺑ ﺒ ﺐ | U+FE8F U+0020 U+FE91 U+0020 U+FE92 U+0020 U+FE90 | Q2 (isolated/initial/medial/final beh) |
| P2 | ﻻ \| لا | U+FEFB U+0020 U+007C U+0020 U+0644 U+0627 | Q2+Q3 (lam-alef ligature vs two logical chars) |
| M1 | سعر 100 دولار | (mixed) | Numerals inside Arabic |
| M2 | abc مرحبا 123 | (mixed) | Latin/Arabic mixing |
| T1 | مـــرحبا | U+0645 U+0640 U+0640 U+0640 U+0631 U+062D U+0628 U+0627 | Q1 (tatweel U+0640) |

## Observations

Judged visually on the Termux screen (screenshots on file).

| ID | Connected? | Artifacts/boxes? | Note |
|---|---|---|---|
| L1 | No | No | Letters isolated, displayed RTL |
| L2 | No | No | Two beh separated |
| P1 | n/a (spaced) | No | All four contextual forms render distinctly and correctly |
| P2 | n/a | No | Ligature renders as single unit; two-char form as two units; visually distinct |
| M1 | No (Arabic part) | No | "100" stays LTR inside RTL text — correct BiDi |
| M2 | No (Arabic part) | No | "abc" LTR left, Arabic RTL middle, "123" LTR right — correct |
| T1 | Partial | No | Tatweel lines visible and extend the word |

## Answers

- **Q1: No.** Termux does not join logical Arabic letters. L1/L2 render as
  isolated forms.
- **Q2: Yes.** Presentation forms render correctly as pre-shaped glyphs.
- **Q3: Yes (visual).** Presentation forms occupy one column each, consistent
  with logical letters. No width anomaly observed.

Additional finding: Termux applies the Unicode BiDi algorithm (text displays
right-to-left in correct order) but performs no glyph shaping. Ordering works;
joining does not.

## Decision

Per the pre-registered rules: L1 not connected + P1 connected → **the need
for an explicit shaping policy is established.**

This satisfies the condition in `docs/rtl-feed-integration.md` for
considering a shaping policy. Any such policy must be:

1. An explicit, documented decision (not an implicit side effect of BiDi),
2. Accompanied by a golden corpus and regression tests per project rules,
3. Aware that presentation forms copy/search poorly (logical source must
   remain canonical for journal, search, and replay).

## Artifacts

- Probe: `tools/termux-shaping-probe/probe.py`
- Raw output: `run-20261010-1751.txt` (phone)
- Screenshots: on file (phone)
