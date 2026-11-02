<!-- claude-voix:debut — bloc géré par TextToSpeech_claude/install.sh -->
# Format des réponses (lecture vocale)

Mes réponses finales sont lues à voix haute (gestionnaire claude-voix, voix locale). Seule la partie avant la première ligne `---` est lue.

## Règle

1. **Partie parlée : 100 mots au plus, en phrases courtes.** Seulement l'essentiel : ce qui est fait, l'état, et la question s'il y en a une (une seule, à la fin). Pas de cause détaillée, de mesure, d'énumération, de chemin, d'URL, de code ni de tableau.
2. **Puis une ligne `---` seule**, systématiquement dès qu'il y a des détails.
3. **Sous la ligne : l'encart technique**, affiché mais pas lu. Causes, mesures, listes, écarts, fichiers, commandes, liens, choix possibles : tout va là, en puces ou en tableau.
4. Jamais de `---` dans la partie parlée : la lecture s'arrête à la première.

## Exemple

Trop long (tout est lu) : un paragraphe sur les causes, la liste des résolutions testées, puis trois paragraphes sur les problèmes du CV.

Bien :

```
J'ai corrigé la hauteur des sections, c'est en ligne sur le NAS et la PR attend ta relecture. Par contre je n'ai pas publié le CV, il contredit deux de nos règles. On le corrige d'abord ?

---

**Hauteur des sections**
- Causes : décalage prévu pour le bandeau live, sections plus courtes que l'écran.
- Vérifié en 1920×1080, 1440×900, 1536×864, 2560×1440 et en prod ; écran ≤ 768 px de haut : quelques pixels de trop.

**CV, non publié**
- Affiche le Gmail (le site utilise l'alias anonaddy).
- Nomme le secteur (vote CSE) au lieu de « appli métier critique ».
- Écarts avec le site : binôme, titre, dates de cuisine, CDI/CDD.
```
<!-- claude-voix:fin -->
