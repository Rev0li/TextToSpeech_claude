#!/usr/bin/env bash
# Installe le serveur Kyutai TTS dans kyutai/.venv (Python 3.12, PyTorch CUDA 12.6).
# Usage : kyutai/install.sh
# Prérequis : uv, une carte NVIDIA avec un pilote récent.
set -euo pipefail

ici="$(cd "$(dirname "$0")" && pwd)"
venv="${ici}/.venv"
export UV_HTTP_TIMEOUT="${UV_HTTP_TIMEOUT:-300}"

command -v uv >/dev/null || { echo "uv manquant : https://docs.astral.sh/uv/" >&2; exit 1; }
command -v nvidia-smi >/dev/null || echo "⚠ nvidia-smi introuvable : le serveur tournera sur le processeur (très lent)." >&2

[ -x "${venv}/bin/python" ] || uv venv --python 3.12 "${venv}"

# moshi 0.2.11 impose torch < 2.8 ; la build cu126 garde les cartes Pascal (GTX 10xx).
uv pip install --python "${venv}/bin/python" \
    --index-strategy unsafe-best-match \
    --extra-index-url https://download.pytorch.org/whl/cu126 \
    "moshi==0.2.11" numpy

"${venv}/bin/python" - <<'EOF'
import torch
ok = torch.cuda.is_available()
print("✓ PyTorch", torch.__version__, "| CUDA :", torch.cuda.get_device_name(0) if ok else "non disponible")
EOF

echo "Lancer : ${ici}/kyutai.sh start   (le modèle, ~4 Go, se télécharge au premier lancement)"
