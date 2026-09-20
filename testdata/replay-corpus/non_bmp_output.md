# Scenario: Non-BMP UTF-8 Runes in Output

- **File**: `non_bmp_output.jsonl`
- **Purpose**: Exercises multi-byte UTF-8 emoji and non-BMP symbols, testing StringWidth accounting at terminal boundaries.
- **Covered Features**: non-BMP emoji (🚀, 🛸, ✨, 🛰️), double-width rune rendering
