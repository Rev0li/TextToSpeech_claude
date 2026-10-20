#!/usr/bin/env bash
# Installe la lecture vocale en français des réponses de Claude Code :
# Piper, le plugin tts (version testée 0.1.6), une voix française,
# la config du dépôt et le patch (message final seulement, file d'attente).
#
# Usage : ./install.sh            installation complète
#         ./install.sh --patch    ré-applique seulement le patch (après une mise à jour du plugin)
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
plugin_version="0.1.6"
plugin_dir="${HOME}/.claude/plugins/cache/cadrianmae-claude-marketplace/tts/${plugin_version}"
voice="siwis"

say()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$*" >&2; }

apply_patch() {
    say "Patch du plugin tts ${plugin_version}"
    if [ ! -d "$plugin_dir" ]; then
        warn "Plugin ${plugin_version} introuvable dans ${plugin_dir%/*}."
        warn "Versions présentes : $(ls "${plugin_dir%/*}" 2>/dev/null | tr '\n' ' ')"
        warn "Le patch n'est testé que sur ${plugin_version} : rien n'a été modifié."
        return 1
    fi
    if patch -d "$plugin_dir" -p1 -R --dry-run -s -f < "$here/patches/tts-${plugin_version}.patch" >/dev/null 2>&1; then
        echo "Déjà appliqué."
        return 0
    fi
    # Une ancienne version du patch est peut-être en place : on repart des originaux.
    local f
    for f in hooks/speak.sh hooks/interrupt.sh scripts/lib.sh; do
        [ -f "$plugin_dir/$f.orig" ] && cp "$plugin_dir/$f.orig" "$plugin_dir/$f"
    done
    patch -d "$plugin_dir" -p1 -b -z .orig -s < "$here/patches/tts-${plugin_version}.patch"
    echo "✓ Appliqué (originaux sauvegardés en .orig)."
}

if [ "${1:-}" = "--patch" ]; then
    apply_patch
    exit
fi

say "Prérequis"
missing=()
for c in paplay jq pipx patch curl flock; do command -v "$c" >/dev/null || missing+=("$c"); done
if [ ${#missing[@]} -gt 0 ]; then
    warn "Manquant : ${missing[*]}"
    echo "Fedora : sudo dnf install -y pulseaudio-utils jq pipx patch curl"
    echo "Debian : sudo apt install -y pulseaudio-utils jq pipx patch curl"
    exit 1
fi
echo "✓ OK"

say "Piper"
if command -v piper >/dev/null || [ -x "${HOME}/.local/bin/piper" ]; then
    echo "✓ Déjà installé"
else
    pipx install piper-tts
    command -v piper >/dev/null || pipx ensurepath
fi

say "Plugin tts"
if [ -d "$plugin_dir" ]; then
    echo "✓ Déjà installé (${plugin_version})"
elif command -v claude >/dev/null; then
    claude plugin marketplace add cadrianmae/claude-marketplace || true
    claude plugin install tts@cadrianmae-claude-marketplace
else
    warn "Commande claude introuvable : installe le plugin à la main (README, section 3)."
fi

say "Voix fr_FR-${voice}"
if [ -f "${HOME}/.local/share/piper-voices/fr_FR-${voice}-medium.onnx" ]; then
    echo "✓ Déjà présente"
else
    "$here/install-voice.sh" "$voice" medium
fi

say "Config"
cfg="${HOME}/.claude/.tts-config"
if [ -f "$cfg" ] && ! cmp -s "$cfg" "$here/config/tts-config"; then
    cp "$cfg" "${cfg}.bak"
    echo "Ancienne config sauvegardée dans ${cfg}.bak"
fi
cp "$here/config/tts-config" "$cfg"
echo "✓ ${cfg}"

apply_patch || true

say "Terminé"
echo "Redémarre Claude Code une fois (/exit puis claude), puis teste avec /tts test."
