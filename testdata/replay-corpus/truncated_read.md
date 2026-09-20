# Scenario: Truncated File Read

- **File**: `truncated_read.jsonl`
- **Purpose**: Exercises EventRead rendering warning with truncation symbol (✂) and byte cut count.
- **Covered Features**: read_record, truncated=true, tool_end with TruncatedBytes
