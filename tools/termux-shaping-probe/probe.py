#!/usr/bin/env python3
"""Termux Arabic shaping probe.

Measurement only. Imports nothing from the project, performs no shaping,
does not read or set NABD_RTL. Prints the environment and a fixed matrix of
cases; the human observer judges rendering from the screen and screenshots.

Usage:  python3 probe.py | tee run-$(date +%Y%m%d-%H%M).txt
"""
import os
import platform
import shutil
import subprocess
import sys


def run(cmd, timeout=10):
    try:
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
        out = res.stdout.strip()
        if res.returncode != 0:
            return f"(exit {res.returncode}) {out}".rstrip()
        return out or "(empty)"
    except FileNotFoundError:
        return "(not found)"
    except subprocess.TimeoutExpired:
        return "(timeout)"


def print_environment():
    print("=== ENVIRONMENT ===")
    print(f"python:        {sys.version.split()[0]}")
    print(f"platform:      {platform.platform()}")
    print(f"TERMUX_VERSION env: {os.environ.get('TERMUX_VERSION', '(unset)')}")
    print(f"PREFIX:        {os.environ.get('PREFIX', '(unset)')}")
    for var in ("TERM", "LANG", "LC_ALL", "SHELL"):
        print(f"{var:<14} {os.environ.get(var, '(unset)')}")
    size = shutil.get_terminal_size(fallback=(0, 0))
    print(f"terminal size: {size.columns} cols x {size.lines} lines")
    print()
    print("--- termux-info (raw) ---")
    if shutil.which("termux-info"):
        print(run(["termux-info"], timeout=20))
    else:
        print("termux-info not available on PATH")
    print()


# Q1: logical letters, no transformation.
# Q2: presentation forms, explicit codepoints.
# Q3: column-width observation, judged visually.
CASES = [
    ("L1", "مرحبا"),
    ("L2", "بب"),
    ("P1", "\uFE8F \uFE91 \uFE92 \uFE90"),
    ("P2", "\uFEFB | \u0644\u0627"),
    ("M1", "سعر 100 دولار"),
    ("M2", "abc مرحبا 123"),
    ("T1", "مـــرحبا"),
]


def print_cases():
    print("=== CASES ===")
    for cid, text in CASES:
        print(f"[{cid}] {text}")
        cps = " ".join(f"U+{ord(c):04X}" for c in text)
        print(f"    {cps}")
    print()
    print("Observer record per case: connected? | artifacts/boxes? | note")


def main():
    print_environment()
    print_cases()


if __name__ == "__main__":
    main()
