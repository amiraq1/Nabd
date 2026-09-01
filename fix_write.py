import re

with open("internal/tools/write.go", "r") as f:
    content = f.read()

# Replace function signature and returns for writeFile
content = re.sub(r'func \(w writeFile\) Run\(ctx context\.Context, raw json\.RawMessage\) \(string, error\) {', r'func (w writeFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {', content)
content = re.sub(r'return "", fmt\.Errorf\("وسائط غير صالحة: %w", err\)', r'return "", false, fmt.Errorf("وسائط غير صالحة: %w", err)', content)
content = re.sub(r'return "", fmt\.Errorf\("المحتوى %d بايت، والحد %d", (.*?)\)', r'return "", false, fmt.Errorf("المحتوى %d بايت، والحد %d", \1)', content)
content = re.sub(r'return "", err', r'return "", false, err', content)
content = re.sub(r'return fmt\.Sprintf\("(.*?)"(.*?), nil', r'return fmt.Sprintf("\1"\2, true, nil', content)

# Replace function signature and returns for editFile
content = re.sub(r'func \(w editFile\) Run\(ctx context\.Context, raw json\.RawMessage\) \(string, error\) {', r'func (w editFile) Run(ctx context.Context, raw json.RawMessage) (string, bool, error) {', content)
content = re.sub(r'return "", errors\.New\("(.*?)"\)', r'return "", false, errors.New("\1")', content)

with open("internal/tools/write.go", "w") as f:
    f.write(content)
