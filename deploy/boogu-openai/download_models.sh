#!/usr/bin/env bash
set -euo pipefail

MODEL_DIR="/opt/boogu/models/Boogu-Image-0.1-Turbo"

echo "=== Boogu-Image model weight download ==="

sudo mkdir -p /opt/boogu/models
sudo chown -R "$USER":"$USER" /opt/boogu

if [ -f "$MODEL_DIR/model_index.json" ]; then
    echo "Model weights already present at $MODEL_DIR"
    exit 0
fi

echo "Downloading from ModelScope (primary for domestic servers)..."
python3 -m pip install -q modelscope

modelscope download \
    --model Boogu/Boogu-Image-0.1-Turbo \
    --local_dir "$MODEL_DIR"

if [ ! -f "$MODEL_DIR/model_index.json" ]; then
    echo "ERROR: model_index.json not found. Download may have failed."
    echo "Try Hugging Face as fallback:"
    echo "  python3 -m pip install -U 'huggingface_hub[cli]'"
    echo "  huggingface-cli download Boogu/Boogu-Image-0.1-Turbo --local-dir $MODEL_DIR"
    exit 1
fi

echo "Model weights ready at $MODEL_DIR"
