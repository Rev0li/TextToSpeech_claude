"""Serveur local de synthèse vocale Kyutai TTS, compatible avec l'API OpenAI.

    POST /v1/audio/speech   {"input": "...", "voice": "livre", "response_format": "wav" | "pcm"}
    GET  /v1/voices         voix courtes connues
    GET  /health

- `wav` : fichier WAV complet (PCM 16 bits, 24 kHz, mono), rendu une fois le texte généré.
- `pcm` : PCM brut s16le 24 kHz mono, envoyé morceau par morceau (quelques phrases à la fois)
  dès que chacun est prêt. Kyutai peut être plus lent que le temps réel : les pauses tombent
  alors entre les phrases, pas au milieu des mots. Si le client coupe, les morceaux suivants
  ne sont pas générés.

Le modèle reste chargé sur la carte graphique ; une seule génération à la fois.
N'écoute que sur 127.0.0.1.
"""
from __future__ import annotations

import io
import json
import os
import re
import sys
import threading
import time
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import numpy as np
import torch
from moshi.models.loaders import CheckpointInfo
from moshi.models.tts import DEFAULT_DSM_TTS_REPO, TTSModel

HOTE = "127.0.0.1"
PORT = int(os.environ.get("KYUTAI_PORT", "8002"))
APPAREIL = os.environ.get("KYUTAI_DEVICE", "cuda")
NB_FLUX = int(os.environ.get("KYUTAI_NQ", "32"))  # moins = plus rapide, moins fidèle
MAX_CARACTERES = 8000
MAX_CORPS = 64 * 1024

# Découpage du mode pcm : premier morceau court (la voix démarre vite), les suivants plus
# longs (chaque génération coûte ~2 s fixes).
PREMIER_MORCEAU = 30
MORCEAU = 100

# Noms courts → voix du dépôt Hugging Face kyutai/tts-voices.
VOIX = {
    "livre": "cml-tts/fr/10087_11650_000028-0002_enhanced.wav",
    "developpeuse": "unmute-prod-website/developpeuse-3.wav",
    "fabien": "unmute-prod-website/fabieng-enhanced-v2.wav",
    "developpeur": "unmute-prod-website/developer-1.mp3",
}
VOIX_DEFAUT = os.environ.get("KYUTAI_VOICE", "livre")


def journal(message: str) -> None:
    print(f"[kyutai {time.strftime('%H:%M:%S')}] {message}", file=sys.stderr, flush=True)


def decouper(texte: str) -> list[str]:
    """Regroupe les phrases en morceaux : un premier court (il peut s'arrêter à une virgule),
    puis d'environ MORCEAU caractères."""
    morceaux: list[str] = []
    courant = ""
    for phrase in (p for p in re.split(r"(?<=[.!?…:;])\s+|\n+", texte) if p.strip()):
        if not morceaux and not courant and len(phrase) > PREMIER_MORCEAU:
            debut, virgule, reste = phrase.partition(", ")
            if virgule and len(debut) >= 15:
                morceaux.append(debut + ",")
                phrase = reste
        courant = f"{courant} {phrase}".strip()
        if len(courant) >= (PREMIER_MORCEAU if not morceaux else MORCEAU):
            morceaux.append(courant)
            courant = ""
    if courant:
        morceaux.append(courant)
    return morceaux


class Synthese:
    def __init__(self) -> None:
        t0 = time.time()
        self.tts = TTSModel.from_checkpoint_info(
            CheckpointInfo.from_hf_repo(DEFAULT_DSM_TTS_REPO),
            n_q=NB_FLUX,
            temp=0.6,
            device=APPAREIL,
        )
        self.frequence = self.tts.mimi.sample_rate
        self.verrou = threading.Lock()
        self.conditions: dict[str, object] = {}
        journal(f"modèle chargé en {time.time() - t0:.1f} s sur {APPAREIL}")

    def chemin_voix(self, voix: str | None) -> str:
        """Nom court, ou chemin du dépôt de voix (contient un « / »). Inconnu → voix par défaut."""
        if voix and voix in VOIX:
            return VOIX[voix]
        if voix and "/" in voix and ".." not in voix:
            return voix
        if voix:
            journal(f"voix « {voix} » inconnue, voix par défaut utilisée")
        return VOIX.get(VOIX_DEFAUT, VOIX_DEFAUT)

    def condition(self, chemin: str):
        if chemin not in self.conditions:
            self.conditions[chemin] = self.tts.make_condition_attributes(
                [self.tts.get_voice_path(chemin)], cfg_coef=2.0
            )
        return self.conditions[chemin]

    def generer(self, texte: str, voix: str | None) -> bytes:
        """PCM s16le du texte entier."""
        with self.verrou:
            chemin = self.chemin_voix(voix)
            cond = self.condition(chemin)
            entrees = self.tts.prepare_script([texte], padding_between=1)
            t0 = time.time()
            morceaux: list[bytes] = []

            def sur_trame(trame):
                if (trame != -1).all():
                    pcm = self.tts.mimi.decode(trame[:, 1:, :]).cpu().numpy()[0, 0]
                    morceaux.append((np.clip(pcm, -1, 1) * 32767).astype("<i2").tobytes())

            with self.tts.mimi.streaming(1), torch.no_grad():
                self.tts.generate([entrees], [cond], on_frame=sur_trame)
            pcm = b"".join(morceaux)
            journal(f"{len(pcm) / 2 / self.frequence:.1f} s d'audio en {time.time() - t0:.1f} s")
            return pcm


class Gestionnaire(BaseHTTPRequestHandler):
    synthese: Synthese
    protocol_version = "HTTP/1.1"

    def log_message(self, format, *args):  # noqa: A002 — journal plus sobre
        pass

    def repondre_json(self, code: int, donnees: dict) -> None:
        corps = json.dumps(donnees, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(corps)))
        self.end_headers()
        self.wfile.write(corps)

    def erreur(self, code: int, message: str) -> None:
        self.repondre_json(code, {"error": {"message": message}})

    def do_GET(self):
        if self.path == "/health":
            self.repondre_json(200, {"status": "ok"})
        elif self.path in ("/v1/voices", "/v1/audio/voices"):
            self.repondre_json(200, {"voices": list(VOIX), "default": VOIX_DEFAUT})
        elif self.path == "/v1/models":
            self.repondre_json(200, {"object": "list", "data": [{"id": "kyutai-tts", "object": "model"}]})
        else:
            self.erreur(404, "Chemin inconnu.")

    def do_POST(self):
        if self.path != "/v1/audio/speech":
            return self.erreur(404, "Chemin inconnu.")
        taille = int(self.headers.get("Content-Length") or 0)
        if not 0 < taille <= MAX_CORPS:
            return self.erreur(413, "Corps absent ou trop gros.")
        try:
            demande = json.loads(self.rfile.read(taille))
        except json.JSONDecodeError:
            return self.erreur(400, "JSON invalide.")
        texte = str(demande.get("input") or "").strip()
        if not texte:
            return self.erreur(400, "Champ « input » vide.")
        if len(texte) > MAX_CARACTERES:
            return self.erreur(413, f"Texte limité à {MAX_CARACTERES} caractères.")
        format_ = demande.get("response_format") or "wav"
        if format_ not in ("wav", "pcm"):
            return self.erreur(400, "response_format : « wav » ou « pcm ».")
        voix = demande.get("voice")

        if format_ == "pcm":
            return self.flux_pcm(texte, voix)

        try:
            pcm = self.synthese.generer(texte, voix)
        except Exception as erreur:
            journal(f"échec de génération : {erreur!r}")
            return self.erreur(500, "La génération a échoué.")
        tampon = io.BytesIO()
        with wave.open(tampon, "wb") as wav:
            wav.setnchannels(1)
            wav.setsampwidth(2)
            wav.setframerate(self.synthese.frequence)
            wav.writeframes(pcm)
        corps = tampon.getvalue()
        self.send_response(200)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("Content-Length", str(len(corps)))
        self.end_headers()
        self.wfile.write(corps)

    def flux_pcm(self, texte: str, voix: str | None) -> None:
        self.send_response(200)
        self.send_header("Content-Type", "audio/pcm")
        self.send_header("X-Sample-Rate", str(self.synthese.frequence))
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        try:
            for morceau in decouper(texte):
                pcm = self.synthese.generer(morceau, voix)
                self.wfile.write(b"%x\r\n%s\r\n" % (len(pcm), pcm))
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
        except (BrokenPipeError, ConnectionResetError):
            # Détecté à l'envoi d'un morceau : les suivants ne sont pas générés.
            journal("lecture coupée par le client, génération arrêtée")
        except Exception as erreur:  # le client reçoit un flux tronqué, le serveur continue
            journal(f"échec de génération : {erreur!r}")
            self.close_connection = True


def main() -> None:
    Gestionnaire.synthese = Synthese()
    serveur = ThreadingHTTPServer((HOTE, PORT), Gestionnaire)
    journal(f"prêt sur http://{HOTE}:{PORT} (voix par défaut : {VOIX_DEFAUT})")
    try:
        serveur.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
