# Day 1 Frictions & Notes

*Write down any friction, UI quirk, or architectural limit encountered during real-world usage here. Don't fix it immediately; collect observations to shape v1.1.*

- (Empty for now. Awaiting first real-world session.)
- **Friction 1**: The default model `qwen/qwen3-coder-480b-a35b-instruct` was hardcoded and reached EOL, causing a 410 error on startup. We need to either select a more stable default model, dynamically fetch available models, or handle 410 gracefully by falling back.
- **Friction 2**: The banner in `main.go` was hardcoded to `nabd v0.2` and was not updated with the releases, causing confusion about the running version.
قائمة الموديلات من /models هي كتالوج فقط ولا تعني الصلاحية للاستخدام، والصلاحية والقدرة على استدعاء الأدوات لا تُعرفان إلا بطلب فعلي.
