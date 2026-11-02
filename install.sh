#!/usr/bin/env bash
# Installe la lecture vocale en français des réponses de Claude Code :
# Piper, le plugin tts (version testée 0.1.6), une voix française,
# la config du dépôt, le patch et le gestionnaire de conversation claude-voix.
#
# Usage : ./install.sh             installation complète (voix Piper)
#         ./install.sh --kyutai    idem + voix Kyutai (carte NVIDIA, ~4 Go de mémoire vidéo)
#         ./install.sh --patch     ré-applique seulement le patch (après une mise à jour du plugin)
#         ./install.sh --voix      recompile seulement claude-voix (après une mise à jour du dépôt)
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
plugin_version="0.1.6"
plugin_dir="${HOME}/.claude/plugins/cache/cadrianmae-claude-marketplace/tts/${plugin_version}"
voice="siwis"

say()  { printf '\n\033[1;36m▸ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$*" >&2; }

# Écrit CLÉ=valeur dans la config tts, en gardant les autres clés.
set_cfg() {
    local cfg="${HOME}/.claude/.tts-config"
    if grep -q "^$1=" "$cfg" 2>/dev/null; then
        sed -i "s|^$1=.*|$1=$2|" "$cfg"
    else
        printf '%s=%s\n' "$1" "$2" >> "$cfg"
    fi
}

# Compile le gestionnaire claude-voix (Go) dans ~/.local/bin.
build_voix() {
    say "Gestionnaire de conversation claude-voix"
    if ! command -v go >/dev/null; then
        warn "Go introuvable : claude-voix n'est pas installé, les réponses seront lues directement."
        echo "Pour l'avoir : sudo dnf install -y golang   (Debian : sudo apt install -y golang)   puis ./install.sh --voix"
        return 1
    fi
    mkdir -p "${HOME}/.local/bin"
    (cd "$here/voix" && go build -trimpath -ldflags "-s -w" -o "${HOME}/.local/bin/claude-voix" .)
    set_cfg GESTIONNAIRE true
    echo "✓ ${HOME}/.local/bin/claude-voix"
}

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

case "${1:-}" in
    --patch) apply_patch; exit ;;
    --voix)  build_voix; exit ;;
    --kyutai|"") ;;
    *) echo "Usage : $0 [--kyutai | --patch | --voix]" >&2; exit 1 ;;
esac

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

gestionnaire=0
build_voix && gestionnaire=1

if [ "${1:-}" = "--kyutai" ]; then
    say "Voix Kyutai"
    "$here/kyutai/install.sh"
    set_cfg ENGINE kyutai
    set_cfg KYUTAI_SCRIPT "$here/kyutai/kyutai.sh"
    echo "✓ Kyutai configuré (claude-voix démarre et arrête le serveur tout seul)"
fi

say "Terminé"
echo "1. Redémarre Claude Code une fois (/exit puis claude)."
if [ "$gestionnaire" = 1 ]; then
    echo "2. Dans un autre terminal, lance : claude-voix"
    echo "   Les réponses de Claude Code y arrivent et sont lues. Fermé, rien n'est lu."
else
    echo "2. Teste avec /tts test."
fi
