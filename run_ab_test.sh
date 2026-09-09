set -e
BIN_DIR="$(mktemp -d "$HOME/nabd-output-test.XXXXXX")"
go build -ldflags="-X main.version=experiment-output-policy" -o "$BIN_DIR/ag" ./cmd/ag
export BIN_DIR

rm -f /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/groq_results.txt
rm -f /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/nvidia_results.txt

python3 /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/run_eval.py /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/config_groq /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/groq_results.txt

python3 /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/run_eval.py /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/config_nvidia /data/data/com.termux/files/home/.gemini/antigravity-cli/brain/8eacab7d-d143-4de0-aa9f-4a7430346ae4/scratch/nvidia_results.txt
