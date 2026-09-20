# Scenario: Trailing Text Event as the Last Event

- **File**: `trailing_text.jsonl`
- **Purpose**: Exercises advance()'s final buffer flush when *m.buf != '' at session EOF (the single most bug-prone path in Replay).
- **Covered Features**: run_start, user_msg, turn_start, text_delta, trailing EOF without turn_end
