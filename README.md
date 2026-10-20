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
./install.sh
```

Le script installe Piper, le plugin `tts`, la voix `fr_FR-siwis`, copie [`config/tts-config`](./config/tts-config) dans `~/.claude/.tts-config` (l'ancienne config est gardée en `.bak`) et applique le [patch](#11-le-patch-message-final-file-dattente). Il ne refait pas ce qui est déjà en place, tu peux donc le relancer sans risque.

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

- `hooks/speak.sh` : ne lit que le **dernier message** du tour, c'est-à-dire le résumé final ;
- `scripts/lib.sh` : avant la lecture, retire les liens, les adresses web, les chemins de fichiers (`~/…`, `/home/…`, `/tmp/…`), les lignes de tableau et les puces ;
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

---

## Crédits

- Plugin `tts` : [cadrianmae/claude-marketplace](https://github.com/cadrianmae/claude-marketplace) (MIT)
- Moteur : [Piper](https://github.com/rhasspy/piper) ; voix : [rhasspy/piper-voices](https://huggingface.co/rhasspy/piper-voices). Chaque voix a sa propre licence, indiquée dans sa fiche Hugging Face.

Ce dépôt contient de la documentation, des scripts d'installation, une config et un patch de quelques lignes ; il ne redistribue ni le plugin ni les voix.

## Licence

[MIT](./LICENSE) : libre de copier, modifier et redistribuer, y compris pour un usage commercial.
