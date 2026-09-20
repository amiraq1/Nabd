# Scenario: Streaming Text Mid-Session

- **File**: `streaming_text.jsonl`
- **Purpose**: Exercises text delta accumulation in Replay.buf and flushing via flushJoin when a non-delta event arrives.
- **Covered Features**: run_start, user_msg, turn_start, text_delta, turn_end, run_end
