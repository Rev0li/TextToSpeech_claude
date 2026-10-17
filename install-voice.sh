#!/usr/bin/env bash
# Télécharge une voix Piper française dans le dossier lu par le plugin tts.
# Usage : ./install-voice.sh <voix> [qualité]
#   ex.   ./install-voice.sh siwis medium
#         ./install-voice.sh upmc
set -euo pipefail

voice="${1:-}"
quality="${2:-medium}"
dest="${HOME}/.local/share/piper-voices"
base="https://huggingface.co/rhasspy/piper-voices/resolve/main/fr/fr_FR"

if [ -z "$voice" ]; then
    echo "Usage : $0 <voix> [qualité]   (voix : siwis, upmc, tom, gilles, mls, mls_1840)" >&2
    exit 1
fi

name="fr_FR-${voice}-${quality}"
mkdir -p "$dest"

for f in "${name}.onnx" "${name}.onnx.json"; do
    echo "↓ $f"
    curl -fL --progress-bar -o "${dest}/${f}.part" "${base}/${voice}/${quality}/${f}" \
        || { rm -f "${dest}/${f}.part"; echo "Échec : $f n'existe pas (voix ou qualité invalide ?)" >&2; exit 1; }
    mv "${dest}/${f}.part" "${dest}/${f}"
done

echo "✓ Installée dans ${dest}"
echo "  Nom à utiliser : fr_FR-${voice}   →   /tts voice fr_FR-${voice}"
if command -v jq >/dev/null 2>&1; then
    speakers="$(jq -r '.speaker_id_map // {} | keys | join(", ")' "${dest}/${name}.onnx.json")"
    [ -n "$speakers" ] && echo "  Locuteurs : ${speakers}   →   /tts voice fr_FR-${voice}:<locuteur>"
fi
