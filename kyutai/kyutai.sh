#!/usr/bin/env bash
# Pilote le serveur Kyutai TTS local.
# Usage : kyutai.sh start | stop | restart | status | logs | dis "texte" [voix]
set -euo pipefail

ici="$(cd "$(dirname "$0")" && pwd)"
port="${KYUTAI_PORT:-8002}"
url="http://127.0.0.1:${port}"
etat="${XDG_RUNTIME_DIR:-/tmp}/kyutai-tts.pid"
log="${XDG_CACHE_HOME:-$HOME/.cache}/kyutai-tts/serveur.log"

actif() { [ -f "$etat" ] && kill -0 "$(cat "$etat")" 2>/dev/null; }

case "${1:-}" in
start)
    if actif; then echo "Déjà lancé (pid $(cat "$etat"))."; exit 0; fi
    [ -x "${ici}/.venv/bin/python" ] || { echo "Lance d'abord ${ici}/install.sh" >&2; exit 1; }
    mkdir -p "$(dirname "$log")"
    # torch.compile ne gère pas les cartes trop anciennes (Pascal) : on s'en passe.
    NO_TORCH_COMPILE=1 KYUTAI_PORT="$port" \
        nohup "${ici}/.venv/bin/python" "${ici}/serveur.py" >>"$log" 2>&1 &
    echo $! >"$etat"
    printf "Chargement du modèle"
    for _ in $(seq 1 600); do
        if curl -fs "${url}/health" >/dev/null; then echo; echo "✓ Prêt sur ${url}"; exit 0; fi
        actif || { echo; echo "✗ Le serveur s'est arrêté, voir : $log" >&2; exit 1; }
        printf "."; sleep 1
    done
    echo; echo "✗ Pas prêt après 10 min, voir : $log" >&2; exit 1
    ;;
stop)
    if actif; then kill "$(cat "$etat")"; rm -f "$etat"; echo "Arrêté."; else echo "Pas lancé."; fi
    ;;
restart) "$0" stop; "$0" start ;;
status)
    if actif && curl -fs "${url}/health" >/dev/null; then echo "✓ Prêt sur ${url} (pid $(cat "$etat"))"
    elif actif; then echo "… en cours de chargement (pid $(cat "$etat"))"
    else echo "Arrêté."; exit 1; fi
    ;;
logs) tail -n 50 -f "$log" ;;
dis)
    texte="${2:?Usage : $0 dis \"texte\" [voix]}"
    corps="$(jq -n --arg t "$texte" --arg v "${3:-}" '{input:$t, response_format:"pcm"} + (if $v=="" then {} else {voice:$v} end)')"
    curl -fsN "${url}/v1/audio/speech" -H 'Content-Type: application/json' -d "$corps" \
        | paplay --raw --format=s16le --rate=24000 --channels=1
    ;;
*)
    echo "Usage : $0 start | stop | restart | status | logs | dis \"texte\" [voix]" >&2; exit 1 ;;
esac
