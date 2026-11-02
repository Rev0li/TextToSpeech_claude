# TextToSpeech_claude

Faire lire à voix haute, **en français**, les réponses de [Claude Code](https://claude.com/claude-code), en local et hors ligne.

On s'appuie sur :
- le plugin [`tts`](https://github.com/cadrianmae/claude-marketplace) de **cadrianmae**, qui lit chaque réponse de Claude et s'arrête dès que tu tapes un nouveau message ;
- [Piper](https://github.com/rhasspy/piper), un moteur de synthèse vocale libre qui tourne sur ta machine (aucune donnée n'est envoyée en ligne) ;
- une voix française Piper, `siwis` ou `upmc`.

> Testé sur **Fedora 44** (PipeWire), Claude Code 2.1, piper-tts 1.8.0, plugin tts 0.1.6.
> Ça devrait marcher sur toute distribution Linux qui utilise PipeWire ou PulseAudio.

---

## Installation rapide

```bash
git clone https://github.com/Rev0li/TextToSpeech_claude.git
cd TextToSpeech_claude
./install.sh            # voix Piper (tout ordinateur)
./install.sh --kyutai   # ou : voix Kyutai, plus naturelle (carte NVIDIA)
```

Puis redémarre Claude Code une fois et, dans un autre terminal, lance **`claude-voix`** : les réponses de Claude Code y arrivent et sont lues à voix haute (voir [section 13](#13-claude-voix-le-gestionnaire-de-conversation)).

Le script installe Piper, le plugin `tts`, la voix `fr_FR-siwis`, copie [`config/tts-config`](./config/tts-config) dans `~/.claude/.tts-config` (l'ancienne config est gardée en `.bak`), applique le [patch](#11-le-patch-message-final-file-dattente), ajoute la [consigne de format](./config/CLAUDE-voix.md) des réponses à `~/.claude/CLAUDE.md` et compile le gestionnaire `claude-voix` (il faut Go : `sudo dnf install -y golang`). Avec `--kyutai`, il installe aussi le serveur Kyutai. Il ne refait pas ce qui est déjà en place, tu peux donc le relancer sans risque. Après une mise à jour du dépôt : `git pull` puis `./install.sh --voix` (recompile `claude-voix`) et `./install.sh --patch`.

Le reste de ce README détaille chaque étape, si tu préfères tout faire à la main.

---

## Sommaire

1. [Prérequis](#1-prérequis)
2. [Installer Piper](#2-installer-piper)
3. [Installer le plugin dans Claude Code](#3-installer-le-plugin-dans-claude-code)
4. [Télécharger une voix française](#4-télécharger-une-voix-française)
5. [Configurer le plugin](#5-configurer-le-plugin)
6. [Tester](#6-tester)
7. [Commandes `/tts`](#7-commandes-tts)
8. [Choisir d'autres voix](#8-choisir-dautres-voix)
9. [Dépannage](#9-dépannage)
10. [Désinstaller](#10-désinstaller)
11. [Le patch (message final, file d'attente)](#11-le-patch-message-final-file-dattente)
12. [Voix Kyutai (serveur local, carte NVIDIA)](#12-voix-kyutai-serveur-local-carte-nvidia)
13. [claude-voix, le gestionnaire de conversation](#13-claude-voix-le-gestionnaire-de-conversation)

---

## 1. Prérequis

Il te faut trois outils :

| Outil | À quoi il sert |
|---|---|
| `paplay` | joue le son produit par Piper |
| `jq` | permet au plugin de lire les réponses de Claude |
| `pipx` | installe Piper dans un environnement Python isolé |

Vérifie ce qui est déjà installé :

```bash
for c in paplay jq pipx; do printf '%-7s ' $c; command -v $c || echo "MANQUANT"; done
```

Installe ce qui manque :

```bash
# Fedora
sudo dnf install -y pulseaudio-utils jq pipx

# Debian / Ubuntu
sudo apt install -y pulseaudio-utils jq pipx

# Arch
sudo pacman -S --needed libpulse jq python-pipx
```

`pandoc` est facultatif. S'il est installé, le plugin s'en sert pour mieux retirer la mise en forme Markdown avant la lecture.

## 2. Installer Piper

```bash
pipx install piper-tts
command -v piper || pipx ensurepath   # puis ouvre un nouveau terminal
```

`command -v piper` doit afficher un chemin du genre `~/.local/bin/piper`.

## 3. Installer le plugin dans Claude Code

Depuis un terminal :

```bash
claude plugin marketplace add cadrianmae/claude-marketplace
claude plugin install tts@cadrianmae-claude-marketplace
```

Si ta version de Claude Code ne connaît pas ces commandes, tape ceci dans Claude Code :

```
/plugin marketplace add cadrianmae/claude-marketplace
/plugin install tts@cadrianmae-claude-marketplace
```

## 4. Télécharger une voix française

Le plugin cherche les voix dans **`~/.local/share/piper-voices/`**. Chaque voix se compose de deux fichiers, `.onnx` et `.onnx.json`, et les deux sont nécessaires.

Le script [`install-voice.sh`](./install-voice.sh) les télécharge pour toi :

```bash
./install-voice.sh siwis medium    # voix féminine (celle qu'on utilise par défaut ici)
./install-voice.sh upmc medium     # 2 voix : jessica (femme) et pierre (homme)
```

Tu peux aussi les télécharger à la main :

```bash
v=siwis q=medium
mkdir -p ~/.local/share/piper-voices && cd ~/.local/share/piper-voices
for f in fr_FR-$v-$q.onnx fr_FR-$v-$q.onnx.json; do
  curl -fLO "https://huggingface.co/rhasspy/piper-voices/resolve/main/fr/fr_FR/$v/$q/$f"
done
```

### Le nom à utiliser dans le plugin

Le plugin désigne chaque voix par un **nom court** : le nom du fichier sans `-medium`, `-low` ni `-high`.

| Fichier | Nom court |
|---|---|
| `fr_FR-siwis-medium.onnx` | `fr_FR-siwis` |
| `fr_FR-upmc-medium.onnx` | `fr_FR-upmc` (ou `fr_FR-upmc:pierre`, `fr_FR-upmc:jessica`) |
| `fr_FR-tom-medium.onnx` | `fr_FR-tom` |

> ⚠️ N'installe pas deux qualités de la même voix (par exemple `siwis-low` et `siwis-medium`) : elles auraient le même nom court et le plugin n'en verrait qu'une seule.

## 5. Configurer le plugin

La configuration se trouve dans **`~/.claude/.tts-config`**, un simple fichier texte au format `CLÉ=valeur`.

Le plugin est **désactivé à l'installation**. Pour l'activer en français, crée ce fichier :

```bash
cat > ~/.claude/.tts-config <<'EOF'
VOICE=fr_FR-siwis
VOLUME=40000
SPEAK_MODE=truncate
MAX_CHARS=1000
TTS_ENABLED=true
INTERRUPT_ON_TYPE=true
SPEED=1.0
EXPRESSIVENESS=0.667
PRONUNCIATION_VARIATION=0.8
SENTENCE_SILENCE=0.0
CHIME_ENABLED=true
CHIME_SOUND=soft-chime
EOF
```

Tu peux aussi le faire depuis Claude Code, avec `/tts auto on` puis `/tts voice fr_FR-siwis`.

| Clé | Valeurs | Effet |
|---|---|---|
| `VOICE` | nom court | voix utilisée |
| `TTS_ENABLED` | `true` / `false` | active ou coupe la lecture |
| `SPEAK_MODE` | `full` / `truncate` / `summary` | voir ci-dessous |
| `MAX_CHARS` | nombre | nombre maximal de caractères lus (modes `truncate` et `summary`) |
| `VOLUME` | 0 – 65536 | volume, qu'on peut seulement **baisser** : au-dessus de la valeur par défaut, le son sature |
| `SPEED` | 0.1 – 3.0 | vitesse : **en dessous de 1.0 c'est plus rapide**, au-dessus c'est plus lent |
| `INTERRUPT_ON_TYPE` | `true` / `false` | arrête la lecture dès que tu tapes un message |
| `CHIME_ENABLED` / `CHIME_SOUND` | `true`/`false`, `soft-chime`/`bell`/`pip`/`hum` | petit son joué avant chaque lecture |

**Modes de lecture :**
- `full` : lit toute la réponse, sans les blocs de code ;
- `truncate` (par défaut) : lit les `MAX_CHARS` premiers caractères ;
- `summary` : quand la réponse dépasse `MAX_CHARS`, la fait résumer par Claude Haiku avant de la lire. ⚠️ Ce mode **consomme des crédits** sur ton compte.

### Redémarrer Claude Code, une seule fois

Le plugin ne se charge qu'au démarrage d'une session. Après l'installation, quitte Claude Code (`/exit`) puis relance `claude`.

Ensuite, plus besoin de redémarrer : la config est relue à chaque lecture, donc un changement de voix ou de vitesse s'applique tout de suite.

## 6. Tester

Pour tester Piper seul, sans passer par Claude Code :

```bash
echo "Bonjour, la synthèse vocale fonctionne" \
  | piper --model ~/.local/share/piper-voices/fr_FR-siwis-medium.onnx --output-raw 2>/dev/null \
  | paplay --raw --format=s16le --rate=22050 --channels=1
```

Pour une voix qui contient plusieurs locuteurs, ajoute `--speaker N`. Pour upmc, `0` correspond à jessica et `1` à pierre.

Dans Claude Code, lance `/tts test`.

## 7. Commandes `/tts`

| Commande | Effet |
|---|---|
| `/tts` | menu interactif |
| `/tts auto on` / `/tts auto off` | active ou coupe la lecture automatique |
| `/tts test` | joue un échantillon avec la voix actuelle |
| `/tts speak <texte>` | lit le texte donné |
| `/tts voices` | liste les voix installées |
| `/tts voice <nom>` | change de voix (ex. `/tts voice fr_FR-upmc:pierre`) |
| `/tts config` | affiche la config |
| `/tts config CLÉ=valeur` | modifie la config (ex. `/tts config SPEAK_MODE=full`) |
| `/tts stop` | ne lit pas la prochaine réponse |

Pour couper une lecture en cours, tape simplement ton message suivant. Avec plusieurs terminaux, ça ne coupe que les réponses du terminal où tu tapes ; `/tts stop` coupe tout et vide la file d'attente (voir [section 11](#11-le-patch-message-final-file-dattente)).

## 8. Choisir d'autres voix

Tu peux écouter toutes les voix sur **https://rhasspy.github.io/piper-samples/** (choisis « French »).

Voix françaises disponibles :

| Voix | Qualité | Locuteurs | Remarque |
|---|---|---|---|
| `siwis` | medium, low | 1 | voix féminine, claire |
| `upmc` | medium | 2 : `jessica`, `pierre` | voix féminine et voix masculine |
| `tom` | medium | 1 | voix masculine |
| `gilles` | low | 1 | voix masculine, qualité basse |
| `mls` | medium | 125 (numérotés) | qualité inégale d'un locuteur à l'autre |
| `mls_1840` | low | 1 | qualité basse |

Pour voir la liste à jour :

```bash
curl -fsSL https://huggingface.co/rhasspy/piper-voices/resolve/main/voices.json \
  | jq -r 'to_entries[] | select(.value.language.code|startswith("fr")) | .key'
```

## 9. Dépannage

**Avertissement `CUDAExecutionProvider is not in available provider names`**
Ce n'est pas grave. Le plugin demande toujours la carte graphique (option `--cuda`) ; sans carte NVIDIA, Piper passe sur le processeur, ce qui suffit largement.

**Aucun son**
- Commence par le test de la [section 6](#6-tester). S'il reste muet, vérifie le son du système avec `paplay /usr/share/sounds/alsa/Front_Center.wav`.
- Vérifie que la config contient bien `TTS_ENABLED=true`.
- Vérifie que la voix est trouvée : `/tts voices` doit afficher le nom indiqué dans `VOICE`.
- Si tu viens d'installer le plugin, redémarre Claude Code.

**`voice '...' not found`**
Le nom indiqué dans `VOICE` doit être le **nom court** (voir [section 4](#le-nom-à-utiliser-dans-le-plugin)). Vérifie aussi que les deux fichiers `.onnx` et `.onnx.json` sont dans `~/.local/share/piper-voices/`.

**La voix lit trop vite ou trop lentement**
Modifie `SPEED`. Attention, le réglage est inversé : `0.9` accélère, `1.2` ralentit.

**Les erreurs détaillées**
Lance `claude --debug` pour voir ce qu'affiche le plugin pendant la lecture.

## 10. Désinstaller

```bash
claude plugin uninstall tts@cadrianmae-claude-marketplace
pipx uninstall piper-tts
rm -rf ~/.local/share/piper-voices ~/.claude/.tts-config
```

## 11. Le patch (message final, file d'attente)

Sans patch, le plugin lit **tout** ce que Claude écrit pendant un tour, y compris les petites phrases entre deux commandes (« Je regarde le fichier… »). Pendant une tâche longue, ça fait beaucoup de bruit.

Autre souci sans patch : avec plusieurs terminaux Claude Code, si deux réponses arrivent en même temps, les deux voix se superposent et on ne comprend plus rien.

Le fichier [`patches/tts-0.1.6.patch`](./patches/tts-0.1.6.patch) change donc ceci :

- `hooks/speak.sh` : ne lit que le **dernier message** du tour, c'est-à-dire le résumé final. Il le prend directement dans ce que Claude Code transmet au hook (`last_assistant_message`) ; avec une version de Claude Code trop ancienne pour fournir ce champ, il le cherche dans la transcription, en attendant jusqu'à 3 s que la réponse y soit écrite ;
- `scripts/lib.sh` : avant la lecture, retire les liens, les adresses web, les chemins de fichiers (`~/…`, `/home/…`, `/tmp/…`), les lignes de tableau et les puces ;
- **encart technique** : une ligne `---` (hors bloc de code) arrête la lecture. Tout ce qui suit s'affiche mais n'est pas lu (voir [section 12](#12-voix-kyutai-serveur-local-carte-nvidia)) ;
- **gestionnaire** : avec `GESTIONNAIRE=true`, chaque réponse part au gestionnaire `claude-voix` au lieu d'être lue directement ; s'il est fermé, rien n'est lu (voir [section 13](#13-claude-voix-le-gestionnaire-de-conversation)) ;
- **moteur Kyutai** : avec `ENGINE=kyutai`, la lecture passe par le serveur Kyutai local, message complet ou au fil de l'eau (`KYUTAI_MODE`) ; s'il ne répond pas, retour à Piper (voir [section 12](#12-voix-kyutai-serveur-local-carte-nvidia)) ;
- **file d'attente** : toutes les lectures, de tous les terminaux, passent l'une après l'autre (verrou `flock` sur `$XDG_RUNTIME_DIR/claude-tts.lock`). Le carillon sonne au début de chaque lecture, quand son tour arrive, ce qui permet de repérer le passage d'une réponse à l'autre ;
- `hooks/interrupt.sh` : taper un message ne coupe que les réponses **de ce terminal** (en cours ou en attente) ; celles des autres terminaux restent dans la file. `/tts stop` coupe tout et vide la file.

Avec `SPEAK_MODE=full` (la config du dépôt), tu entends donc tout le résumé final, et rien d'autre.

Le patch n'est testé que sur la version **0.1.6** du plugin. Une mise à jour du plugin installe une nouvelle version sans le patch : relance alors `./install.sh --patch`. Si la version a changé, le script le signale et ne touche à rien. La même commande remplace aussi une ancienne version du patch par la nouvelle.

Pour annuler le patch, remets les fichiers d'origine (sauvegardés en `.orig`) :

```bash
cd ~/.claude/plugins/cache/cadrianmae-claude-marketplace/tts/0.1.6
for f in hooks/speak.sh hooks/interrupt.sh scripts/lib.sh; do
  [ -f "$f.orig" ] && mv "$f.orig" "$f"
done
```

## 12. Voix Kyutai (serveur local, carte NVIDIA)

Piper est rapide mais sonne « synthèse vocale ». [Kyutai TTS](https://kyutai.org/next/tts) (modèle `kyutai/tts-1.6b-en_fr`, laboratoire français, licence CC-BY 4.0) donne une voix bien plus naturelle, au prix d'une carte graphique. Le dossier [`kyutai/`](./kyutai) le fait tourner comme un **serveur local compatible avec l'API OpenAI** (`/v1/audio/speech`) : tout outil qui sait parler à OpenAI, comme [Alfred](https://github.com/lorloop/Alfred) ou [Cornelius](https://github.com/lfpoulain/Cornelius), peut s'en servir.

```bash
kyutai/install.sh          # environnement Python isolé dans kyutai/.venv (uv requis)
kyutai/kyutai.sh start     # 1er lancement : télécharge le modèle (~4 Go)
kyutai/kyutai.sh dis "Bonjour, la voix Kyutai fonctionne."
kyutai/kyutai.sh stop      # libère la carte graphique
```

Autres commandes : `status`, `restart`, `logs`.

**L'API**, sur `http://127.0.0.1:8002` (n'écoute que sur la machine) :

| Requête | Réponse |
|---|---|
| `POST /v1/audio/speech` `{"input": "…", "voice": "livre", "response_format": "wav"}` | WAV complet, 16 bits, 24 kHz, mono |
| idem avec `"response_format": "pcm"` | PCM brut s16le 24 kHz, envoyé morceau par morceau : un premier court (la voix démarre vite), puis quelques phrases à la fois |
| `GET /v1/voices` | voix courtes connues |
| `GET /health` | `{"status": "ok"}` |

**Lire les réponses de Claude Code avec Kyutai.** Avec le [patch](#11-le-patch-message-final-file-dattente) appliqué, ajoute dans `~/.claude/.tts-config` :

```
ENGINE=kyutai
KYUTAI_URL=http://127.0.0.1:8002
KYUTAI_VOICE=livre
KYUTAI_MODE=complet
```

Si le serveur est arrêté au moment de lire, le plugin retombe sur la voix Piper de `VOICE`.

`KYUTAI_MODE` choisit comment lire :
- `complet` (par défaut) : tout le message est généré d'abord, puis lu d'une traite, sans pause. La génération démarre tout de suite, même pendant la lecture d'une réponse précédente, et le carillon marque le début de la lecture. C'est aussi plus rapide au total (une seule génération au lieu d'une par morceau) ;
- `flux` : la voix démarre plus tôt, mais sur une carte plus lente que le temps réel, des pauses tombent entre les morceaux.

**Réponse parlée + encart technique.** Pour suivre le travail à l'oreille, la réponse de Claude commence par une partie parlée courte (100 mots au plus). Après une ligne `---`, un encart technique (causes, mesures, fichiers, commandes, liens) s'affiche sans être lu. Plus la partie parlée est courte, plus la voix démarre et finit vite.

`install.sh` met cette consigne en place tout seul : il ajoute [`config/CLAUDE-voix.md`](./config/CLAUDE-voix.md) à ton `~/.claude/CLAUDE.md`, entre deux balises `claude-voix`, sans toucher au reste du fichier. Pour la mettre à jour après un `git pull` : `./install.sh --consigne`. Les sessions Claude Code déjà ouvertes ne relisent pas `CLAUDE.md` : relance-les.

Une limite chiffrée (100 mots) marche mieux qu'un simple « court » : sans elle, Claude a tendance à tout mettre dans la partie lue.

Pour Alfred, dans son `.env` : `LOCALAI_TTS_BASE_URL=http://127.0.0.1:8002` et `LOCALAI_TTS_VOICE=livre`.

**Voix.** Onze voix françaises au menu, toutes tirées de livres audio (jeu CML-TTS) :

| Nom | Genre | Timbre |
|---|---|---|
| `livre` (par défaut) | femme | douce, posée |
| `lectrice` | femme | plus claire |
| `fr-2154` | femme | 184 Hz |
| `fr-5476` | femme | 186 Hz |
| `conteur` | homme | chaleureuse |
| `narrateur` | homme | grave |
| `recitant` | homme | plus claire |
| `fr-928` | homme | 133 Hz |
| `fr-1770` | homme | 125 Hz |
| `fr-5790` | homme | 116 Hz |
| `fr-4193` | homme | 85 Hz |

Le plus simple pour les écouter et choisir : la touche `v` de [`claude-voix`](#13-claude-voix-le-gestionnaire-de-conversation).

Un deuxième onglet (touche `tab`), **Plus de voix**, liste les 24 autres voix françaises nettoyées du même jeu, rangées par genre et par hauteur, pour les essayer. Elles sont utilisables telles quelles (`KYUTAI_VOICE=fr-6318`). Pour en ajouter une au menu, il suffit de l'inscrire dans `VOIX_DETAILS` de [`kyutai/serveur.py`](./kyutai/serveur.py) avec un nom et un timbre. On peut aussi donner n'importe quel chemin du dépôt [kyutai/tts-voices](https://huggingface.co/kyutai/tts-voices), par exemple `cml-tts/fr/…_enhanced.wav` (140 voix françaises), sans le suffixe `.1e68beda@240.safetensors`. Une voix inconnue retombe sur la voix par défaut.

**Réglages** (variables d'environnement) : `KYUTAI_PORT` (8002), `KYUTAI_VOICE` (`livre`), `KYUTAI_DEVICE` (`cuda`), `KYUTAI_NQ` (32 ; 16 est environ 30 % plus rapide mais moins fidèle).

**Matériel.** Mesuré sur une GTX 1060 6 Go (Pascal) : ~4 Go de mémoire vidéo au repos, ~5 Go pendant la génération, et environ 1,6 fois plus lent que le temps réel, plus ~2 s par phrase. Sur une carte aussi ancienne, ce serveur ne cohabite pas avec un modèle de langage local. Il convient quand le cerveau tourne ailleurs (Claude Code par exemple). Les cartes récentes (RTX 30xx et plus) vont nettement plus vite. Pour les cartes Pascal, `kyutai.sh` désactive `torch.compile` (`NO_TORCH_COMPILE=1`), que Triton refuse sur ces cartes, et `install.sh` installe PyTorch en build CUDA 12.6, qui les gère encore.

## 13. claude-voix, le gestionnaire de conversation

`claude-voix` est une petite interface en terminal ([Bubble Tea](https://github.com/charmbracelet/bubbletea)) qui reçoit les réponses de toutes tes fenêtres Claude Code, les fait générer, puis les lit l'une après l'autre. Chaque fenêtre a sa **carte** : quand une nouvelle réponse arrive de la même fenêtre, elle remplace l'ancienne (qui est coupée si elle n'était pas finie).

```bash
claude-voix
```

```
🎙 claude-voix   Kyutai prêt (conteur)
╭──────────────────────────────────────╮╭──────────────────────────────────────╮
│ auth ▶ 0:01 / 0:06             15:39 ││ songsurf génération 2s         15:39 │
│ Première réponse de la fenêtre auth, ││ Réponse de SongSurf.                 │
│ assez longue pour…                   ││                                      │
╰──────────────────────────────────────╯╰──────────────────────────────────────╯
espace pause · s suivant · c tout couper · d supprimer · entrée relire · r renommer · q quitter
flèches choisir une carte · échap suivre la lecture
```

Les cartes se rangent en une à trois colonnes selon la largeur du terminal. La carte choisie (bord rose) montre plus de texte, celle en lecture a un bord vert.

| Touche | Effet |
|---|---|
| `espace` (ou `p`) | pause / reprise de la lecture en cours (ou de la file) |
| `s` | passe à la réponse suivante |
| `c` | coupe tout : lecture en cours et réponses en attente |
| `d` (ou `suppr`) | supprime la carte choisie (sa lecture ou sa génération s'arrête) |
| flèches | choisir une carte |
| `échap` | revenir à la carte en cours de lecture |
| `entrée` | relire la réponse de la carte choisie |
| `r` | renommer la fenêtre de la carte choisie |
| `v` | écran **Voix** : écouter les onze voix du menu ou, avec `tab`, les 24 autres (`entrée`) ; choisir celle qui lit les réponses (`espace`) ; la file est en pause pendant les essais |
| `q` | quitter |

**Le nom des fenêtres.** Chaque réponse porte le nom de la fenêtre Claude Code qui l'a écrite : au départ, le nom de son dossier (`rev0auth`, `SongSurf`…). Touche `r` pour le changer (par exemple `auth`). Le nom vaut pour cette session et pour les prochaines sessions lancées dans le même dossier. Il est gardé dans `~/.config/claude-voix/noms.json`.

**Fermé, rien n'est lu.** C'est l'ouverture de `claude-voix` qui déclenche la lecture. Le plugin lui envoie chaque réponse par un socket local (`$XDG_RUNTIME_DIR/claude-voix.sock`, accessible à toi seul). Si `claude-voix` est fermé, la réponse n'est pas lue. Pour revenir à la lecture directe, sans gestionnaire : `GESTIONNAIRE=false` dans `~/.claude/.tts-config`.

**Comportement.**
- Les cartes sont lues dans l'ordre d'arrivée de leur réponse. La suivante est générée pendant que la précédente est lue.
- **La lecture démarre pendant la génération.** Kyutai envoie l'audio au fil de l'eau. `claude-voix` mesure sa vitesse réelle et lance la lecture au moment où elle ne pourra plus rattraper la génération : la voix démarre bien plus tôt, sans aucune pause. Mesuré sur une GTX 1060, pour 19 s de parole : lecture lancée à 14,5 s au lieu de 28 s, tout lu à 33,7 s.
- Supprimer une carte, ou recevoir une nouvelle réponse de la même fenêtre, arrête aussi sa génération : le serveur passe aussitôt à la réponse suivante.
- Taper un message dans une fenêtre Claude Code coupe les réponses de cette fenêtre (comme avant). `/tts stop` coupe tout.
- Avec Kyutai (`./install.sh --kyutai`), `claude-voix` démarre le serveur tout seul s'il est arrêté (`Kyutai démarre…`, les réponses attendent), et l'arrête en quittant pour libérer la carte graphique. Si Kyutai ne répond pas, la voix Piper prend le relais.
- La voix, le volume et le serveur viennent de `~/.claude/.tts-config` (`KYUTAI_VOICE`, `VOLUME`, `VOICE`…), relus à chaque réponse. L'écran `v` écrit `KYUTAI_VOICE` pour toi.

**Commandes internes** (utilisées par le plugin) : `claude-voix envoyer --session ID --cwd DOSSIER < texte` et `claude-voix couper [--session ID]`.

**Recompiler** après une mise à jour : `./install.sh --voix` (les sources sont dans [`voix/`](./voix)).

---

## Crédits

- Plugin `tts` : [cadrianmae/claude-marketplace](https://github.com/cadrianmae/claude-marketplace) (MIT)
- Interface `claude-voix` : [Bubble Tea, Lip Gloss, Bubbles](https://github.com/charmbracelet) de Charm (MIT)
- Moteur : [Piper](https://github.com/rhasspy/piper) ; voix : [rhasspy/piper-voices](https://huggingface.co/rhasspy/piper-voices). Chaque voix a sa propre licence, indiquée dans sa fiche Hugging Face.
- Voix Kyutai : modèle [Kyutai TTS](https://huggingface.co/kyutai/tts-1.6b-en_fr) (CC-BY 4.0), bibliothèque [moshi](https://github.com/kyutai-labs/moshi) (MIT), voix de [kyutai/tts-voices](https://huggingface.co/kyutai/tts-voices) : toutes les voix proposées viennent du jeu [CML-TTS](https://openslr.org/146/) (CC-BY 4.0). Ailleurs dans ce dépôt de voix, certains jeux (`expresso/`, `ears/`) sont **non commerciaux** ; la licence de chaque dossier est indiquée dans son README.

Ce dépôt contient de la documentation, des scripts d'installation, une config et un patch de quelques lignes ; il ne redistribue ni le plugin ni les voix.

## Licence

[MIT](./LICENSE) : libre de copier, modifier et redistribuer, y compris pour un usage commercial.
