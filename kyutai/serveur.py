"""Serveur local de synthèse vocale Kyutai TTS, compatible avec l'API OpenAI.

    POST /v1/audio/speech   {"input": "...", "voice": "livre", "response_format": "wav" | "pcm"}
    GET  /v1/voices         les cinq voix proposées (nom, genre, description)
    GET  /health

- `wav` : fichier WAV complet (PCM 16 bits, 24 kHz, mono), rendu une fois le texte généré.
- `pcm` + `"decoupage": false` : flux continu, une seule génération dont chaque trame (80 ms)
  part dès qu'elle est prête. C'est ce qu'utilise claude-voix.
- `pcm` : PCM brut s16le 24 kHz mono, envoyé morceau par morceau (quelques phrases à la fois)
  dès que chacun est prêt. Kyutai peut être plus lent que le temps réel : les pauses tombent
  alors entre les phrases, pas au milieu des mots.

Si le client ferme la connexion, la génération s'arrête dans la demi-seconde : la place est
libre pour la demande suivante.

Le modèle reste chargé sur la carte graphique ; une seule génération à la fois.
N'écoute que sur 127.0.0.1.
"""
from __future__ import annotations

import io
import json
import os
import re
import select
import socket
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

# Les onze voix du menu (toutes du jeu CML-TTS, livres audio, licence CC-BY 4.0), femmes puis hommes :
# nom court → (chemin dans le dépôt Hugging Face kyutai/tts-voices, genre, description).
# N'importe quel autre chemin du dépôt reste utilisable dans « voice ».
VOIX_DETAILS = {
    "livre": ("cml-tts/fr/10087_11650_000028-0002_enhanced.wav", "femme", "douce, posée"),
    "lectrice": ("cml-tts/fr/5830_4703_000037-0001_enhanced.wav", "femme", "plus claire"),
    "fr-2154": ("cml-tts/fr/2154_2576_000020-0003_enhanced.wav", "femme", "184 Hz"),
    "fr-5476": ("cml-tts/fr/5476_3103_000072-0001_enhanced.wav", "femme", "186 Hz"),
    "conteur": ("cml-tts/fr/2114_1656_000053-0001_enhanced.wav", "homme", "chaleureuse"),
    "narrateur": ("cml-tts/fr/1406_1028_000009-0003_enhanced.wav", "homme", "grave"),
    "recitant": ("cml-tts/fr/4937_3731_000004-0001_enhanced.wav", "homme", "plus claire"),
    "fr-928": ("cml-tts/fr/928_486_000075-0001_enhanced.wav", "homme", "133 Hz"),
    "fr-1770": ("cml-tts/fr/1770_1028_000036-0002_enhanced.wav", "homme", "125 Hz"),
    "fr-5790": ("cml-tts/fr/5790_4893_000052-0001_enhanced.wav", "homme", "116 Hz"),
    "fr-4193": ("cml-tts/fr/4193_3103_000004-0001_enhanced.wav", "homme", "85 Hz"),
}
# Les 24 autres voix françaises nettoyées du même jeu, à tester avant d'en ajouter au menu :
# (nom court, chemin, genre, hauteur médiane en Hz mesurée sur l'extrait de référence).
VOIX_AUTRES = [
    ("fr-7601", "cml-tts/fr/7601_7727_000062-0001_enhanced.wav", "homme", 95),
    ("fr-4482", "cml-tts/fr/4482_3103_000063-0001_enhanced.wav", "homme", 114),
    ("fr-9834", "cml-tts/fr/9834_9697_000150-0003_enhanced.wav", "homme", 114),
    ("fr-2223", "cml-tts/fr/2223_1745_000009-0002_enhanced.wav", "homme", 122),
    ("fr-2216", "cml-tts/fr/2216_1745_000007-0001_enhanced.wav", "homme", 124),
    ("fr-296", "cml-tts/fr/296_1028_000022-0001_enhanced.wav", "homme", 124),
    ("fr-7142", "cml-tts/fr/7142_2432_000124-0003_enhanced.wav", "homme", 133),
    ("fr-4724", "cml-tts/fr/4724_3731_000031-0001_enhanced.wav", "homme", 134),
    ("fr-8128", "cml-tts/fr/8128_7016_000047-0002_enhanced.wav", "homme", 135),
    ("fr-12205", "cml-tts/fr/12205_11650_000004-0002_enhanced.wav", "femme", 180),
    ("fr-7591", "cml-tts/fr/7591_6742_000149-0002_enhanced.wav", "femme", 193),
    ("fr-579", "cml-tts/fr/579_2548_000015-0001_enhanced.wav", "femme", 198),
    ("fr-7400", "cml-tts/fr/7400_2928_000100-0001_enhanced.wav", "femme", 198),
    ("fr-5207", "cml-tts/fr/5207_3078_000031-0002_enhanced.wav", "femme", 199),
    ("fr-7762", "cml-tts/fr/7762_8734_000048-0002_enhanced.wav", "femme", 199),
    ("fr-10177", "cml-tts/fr/10177_10625_000134-0003_enhanced.wav", "femme", 203),
    ("fr-6318", "cml-tts/fr/6318_7016_000027-0002_enhanced.wav", "femme", 203),
    ("fr-12977", "cml-tts/fr/12977_10625_000037-0001_enhanced.wav", "femme", 204),
    ("fr-2465", "cml-tts/fr/2465_1943_000152-0002_enhanced.wav", "femme", 211),
    ("fr-1591", "cml-tts/fr/1591_1028_000108-0004_enhanced.wav", "femme", 213),
    ("fr-577", "cml-tts/fr/577_394_000070-0001_enhanced.wav", "femme", 222),
    ("fr-12080", "cml-tts/fr/12080_11650_000047-0001_enhanced.wav", "femme", 235),
    ("fr-3267", "cml-tts/fr/3267_1902_000075-0001_enhanced.wav", "femme", 242),
    ("fr-10179", "cml-tts/fr/10179_11051_000005-0001_enhanced.wav", "femme", 286),
]
VOIX = {nom: chemin for nom, (chemin, _, _) in VOIX_DETAILS.items()}
VOIX.update({nom: chemin for nom, chemin, _, _ in VOIX_AUTRES})
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


class ClientParti(Exception):
    """Le client a fermé la connexion : inutile de continuer la génération."""


def client_parti(conn: socket.socket) -> bool:
    """Vrai si le client a fermé la connexion. Après la requête, il n'a plus rien à envoyer :
    une socket lisible signifie une fermeture (lecture vide) ou une erreur."""
    try:
        lisible, _, _ = select.select([conn], [], [], 0)
        return bool(lisible) and conn.recv(1, socket.MSG_PEEK) == b""
    except OSError:
        return True


# Vérification toutes les 6 trames (~0,5 s d'audio) : coût négligeable.
TRAMES_ENTRE_VERIFS = 6


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

    def generer(self, texte: str, voix: str | None, parti=None, sortie=None) -> bytes:
        """PCM s16le du texte entier. `parti()` vrai → ClientParti (génération abandonnée).
        Avec `sortie`, chaque trame (80 ms) lui est passée dès qu'elle est prête et rien n'est
        gardé : c'est le flux continu."""
        with self.verrou:
            if parti is not None and parti():
                raise ClientParti
            chemin = self.chemin_voix(voix)
            cond = self.condition(chemin)
            entrees = self.tts.prepare_script([texte], padding_between=1)
            t0 = time.time()
            morceaux: list[bytes] = []
            trames = 0
            total = 0

            def sur_trame(trame):
                nonlocal trames, total
                trames += 1
                if parti is not None and trames % TRAMES_ENTRE_VERIFS == 0 and parti():
                    raise ClientParti
                if (trame != -1).all():
                    pcm = self.tts.mimi.decode(trame[:, 1:, :]).cpu().numpy()[0, 0]
                    octets = (np.clip(pcm, -1, 1) * 32767).astype("<i2").tobytes()
                    total += len(octets)
                    if sortie is not None:
                        sortie(octets)
                    else:
                        morceaux.append(octets)

            with self.tts.mimi.streaming(1), torch.no_grad():
                self.tts.generate([entrees], [cond], on_frame=sur_trame)
            journal(f"{total / 2 / self.frequence:.1f} s d'audio en {time.time() - t0:.1f} s")
            return b"".join(morceaux)


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
            details = [
                {"nom": nom, "genre": genre, "description": description}
                for nom, (_, genre, description) in VOIX_DETAILS.items()
            ]
            autres = [
                {"nom": nom, "genre": genre, "hauteur": hauteur} for nom, _, genre, hauteur in VOIX_AUTRES
            ]
            self.repondre_json(200, {
                "voices": list(VOIX_DETAILS), "default": VOIX_DEFAUT, "details": details, "autres": autres,
            })
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
            return self.flux_pcm(texte, voix, decoupage=demande.get("decoupage", True) is not False)

        try:
            pcm = self.synthese.generer(texte, voix, lambda: client_parti(self.connection))
        except ClientParti:
            journal("génération abandonnée : le client est parti")
            self.close_connection = True
            return
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

    def flux_pcm(self, texte: str, voix: str | None, decoupage: bool = True) -> None:
        self.send_response(200)
        self.send_header("Content-Type", "audio/pcm")
        self.send_header("X-Sample-Rate", str(self.synthese.frequence))
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        parti = lambda: client_parti(self.connection)  # noqa: E731

        def envoyer(octets: bytes) -> None:
            self.wfile.write(b"%x\r\n%s\r\n" % (len(octets), octets))

        try:
            if not decoupage:
                # Flux continu : une seule génération, chaque trame part dès qu'elle est prête.
                self.synthese.generer(texte, voix, parti, envoyer)
                self.wfile.write(b"0\r\n\r\n")
                return
            for morceau in decouper(texte):
                pcm = self.synthese.generer(morceau, voix, lambda: client_parti(self.connection))
                self.wfile.write(b"%x\r\n%s\r\n" % (len(pcm), pcm))
                self.wfile.flush()
            self.wfile.write(b"0\r\n\r\n")
        except (BrokenPipeError, ConnectionResetError, ClientParti):
            journal("lecture coupée par le client, génération arrêtée")
            self.close_connection = True
        except Exception as erreur:  # le client reçoit un flux tronqué, le serveur continue
            journal(f"échec de génération : {erreur!r}")
            self.close_connection = True


class Serveur(ThreadingHTTPServer):
    daemon_threads = True

    def handle_error(self, request, client_address):
        # Client parti (lecture coupée, connexion de vérification fermée) : rien d'anormal.
        if isinstance(sys.exc_info()[1], (BrokenPipeError, ConnectionResetError)):
            return
        super().handle_error(request, client_address)


def main() -> None:
    Gestionnaire.synthese = Synthese()
    serveur = Serveur((HOTE, PORT), Gestionnaire)
    journal(f"prêt sur http://{HOTE}:{PORT} (voix par défaut : {VOIX_DEFAUT})")
    try:
        serveur.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
