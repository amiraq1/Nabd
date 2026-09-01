import re

with open("internal/tools/bash.go", "r") as f:
    content = f.read()

old_spec = """	return spec("bash",
		"ينفّذ أمر صدفة داخل مجلد المشروع. لا مدخلات تفاعلية: أي أمر ينتظر إدخالًا سيرى نهاية ملف فورًا، فمرّر الأعلام غير التفاعلية (-y، --no-input). الأوامر التي تُبقي عمليات في الخلفية تُقتل عند انتهاء الأمر.",
		map[string]any{
			"cmd":       map[string]any{"type": "string", "description": "الأمر كما يُكتب في الصدفة"},
			"timeout_s": map[string]any{"type": "integer", "description": "مهلة بالثواني (افتراضي 120، أقصى 600)"},
		}, "cmd")"""

new_spec = """	return spec("bash",
		"ينفّذ أمر صدفة داخل مجلد المشروع. لا مدخلات تفاعلية: أي أمر ينتظر إدخالًا سيرى نهاية ملف فورًا، فمرّر الأعلام غير التفاعلية (-y، --no-input). الأوامر التي تُبقي عمليات في الخلفية تُقتل عند انتهاء الأمر.",
		`{"type":"object","properties":{"cmd":{"type":"string","description":"الأمر كما يُكتب في الصدفة"},"timeout_s":{"type":"integer","description":"مهلة بالثواني (افتراضي 120، أقصى 600)"}},"required":["cmd"]}`)"""

content = content.replace(old_spec, new_spec)

with open("internal/tools/bash.go", "w") as f:
    f.write(content)
