package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Écran « Voix » (touche v) : écouter les voix proposées par le serveur Kyutai et choisir
// celle qui lit les réponses (KYUTAI_VOICE dans ~/.claude/.tts-config). La file des
// réponses est mise en pause pendant les essais.

const idEssai = -1 // identifiant de lecture réservé aux essais de voix

type infoVoix struct {
	Nom         string `json:"nom"`
	Genre       string `json:"genre"`
	Description string `json:"description"`
	Hauteur     int    `json:"hauteur"`
	Autre       bool   `json:"-"` // hors menu : à tester
}

type listeVoix struct {
	voix []infoVoix
	err  error
}

type essaiPret struct {
	nom string
	err error
}

func chargerVoix() tea.Msg {
	cfg := lireConfig()
	r, err := clientCourt.Get(cfg.KyutaiURL + "/v1/voices")
	if err != nil {
		return listeVoix{err: fmt.Errorf("serveur Kyutai injoignable")}
	}
	defer r.Body.Close()
	var corps struct {
		Details []infoVoix `json:"details"`
		Autres  []infoVoix `json:"autres"`
	}
	if err := json.NewDecoder(r.Body).Decode(&corps); err != nil || len(corps.Details) == 0 {
		return listeVoix{err: fmt.Errorf("liste des voix illisible (serveur Kyutai à jour ?)")}
	}
	// Autres voix : femmes puis hommes, de la plus grave à la plus aiguë.
	sort.SliceStable(corps.Autres, func(i, j int) bool {
		a, b := corps.Autres[i], corps.Autres[j]
		if a.Genre != b.Genre {
			return a.Genre == "femme"
		}
		return a.Hauteur < b.Hauteur
	})
	for i := range corps.Autres {
		corps.Autres[i].Autre = true
		corps.Autres[i].Description = fmt.Sprintf("%d Hz", corps.Autres[i].Hauteur)
	}
	return listeVoix{voix: append(corps.Details, corps.Autres...)}
}

func phraseEssai(nom string) string {
	return fmt.Sprintf("Bonjour, je suis la voix %s. Voici comment je lirai les réponses de Claude Code.", nom)
}

// genererEssai fait dire la phrase d'essai par une voix, dans un tampon gardé pour la réécouter.
func genererEssai(nom string, tp *tampon) tea.Cmd {
	return func() tea.Msg {
		cfg := lireConfig()
		cfg.KyutaiVoix = nom
		ctx, annuler := context.WithTimeout(context.Background(), 2*time.Minute)
		defer annuler()
		err := genererKyutai(ctx, cfg, phraseEssai(nom), tp)
		tp.terminer(err)
		return essaiPret{nom: nom, err: err}
	}
}

// ---- état de l'écran -------------------------------------------------------

type ecranVoix struct {
	ouvert     bool
	voix       []infoVoix
	sel        int
	err        string
	essais     map[string]*tampon
	enCours    string // voix dont l'essai est en génération
	pauseAvant bool   // la file était-elle déjà en pause en ouvrant l'écran ?
}

func (m *modele) ouvrirVoix() tea.Cmd {
	m.ev.ouvert = true
	m.ev.err = ""
	if m.ev.essais == nil {
		m.ev.essais = map[string]*tampon{}
	}
	m.ev.pauseAvant = m.pause
	if !m.pause {
		m.basculerPause()
	}
	return chargerVoix
}

func (m *modele) fermerVoix() tea.Cmd {
	m.ev.ouvert = false
	if m.essai != nil {
		m.essai.arreter()
		m.essai = nil
	}
	if !m.ev.pauseAvant && m.pause {
		return m.basculerPause()
	}
	return nil
}

func (m *modele) ecouterVoix() tea.Cmd {
	if m.ev.sel >= len(m.ev.voix) {
		return nil
	}
	nom := m.ev.voix[m.ev.sel].Nom
	if m.essai != nil {
		m.essai.arreter()
		m.essai = nil
	}
	if tp, ok := m.ev.essais[nom]; ok {
		if audio, fini, err := tp.etat(); fini && err == nil && audio > 0 {
			return m.jouerEssai(tp)
		}
		if m.ev.enCours == nom {
			return nil // déjà en génération
		}
	}
	tp := nouveauTampon()
	m.ev.essais[nom] = tp
	m.ev.enCours = nom
	return genererEssai(nom, tp)
}

func (m *modele) jouerEssai(tp *tampon) tea.Cmd {
	l, attendre, err := demarrerLecture(idEssai, tp, lireConfig().Volume)
	if err != nil {
		m.ev.err = "paplay : " + err.Error()
		return nil
	}
	m.essai = l
	return attendre
}

func (m *modele) choisirVoix() {
	if m.ev.sel >= len(m.ev.voix) {
		return
	}
	if err := ecrireConfig("KYUTAI_VOICE", m.ev.voix[m.ev.sel].Nom); err != nil {
		m.ev.err = "écriture de la config : " + err.Error()
	}
}

func (m *modele) majVoix(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case listeVoix:
		if msg.err != nil {
			m.ev.err = msg.err.Error()
			return nil, true
		}
		m.ev.voix = msg.voix
		actuelle := lireConfig().KyutaiVoix
		for i, v := range m.ev.voix {
			if v.Nom == actuelle {
				m.ev.sel = i
			}
		}
		return nil, true
	case essaiPret:
		if m.ev.enCours == msg.nom {
			m.ev.enCours = ""
		}
		if msg.err != nil {
			m.ev.err = msg.nom + " : " + msg.err.Error()
			delete(m.ev.essais, msg.nom)
			return nil, true
		}
		// On ne joue que si l'écran est ouvert et que cette voix est toujours choisie.
		if m.ev.ouvert && m.ev.sel < len(m.ev.voix) && m.ev.voix[m.ev.sel].Nom == msg.nom && m.essai == nil {
			return m.jouerEssai(m.ev.essais[msg.nom]), true
		}
		return nil, true
	case finLecture:
		if msg.id == idEssai {
			m.essai = nil
			return nil, true
		}
	}
	return nil, false
}

func (m *modele) toucheVoix(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "v":
		return m, m.fermerVoix()
	case "up", "k":
		if m.ev.sel > 0 {
			m.ev.sel--
		}
	case "down", "j":
		if m.ev.sel < len(m.ev.voix)-1 {
			m.ev.sel++
		}
	case "enter":
		return m, m.ecouterVoix()
	case " ":
		m.choisirVoix()
	}
	return m, nil
}

// ---- affichage -------------------------------------------------------------

func (m *modele) vueVoix(l int) string {
	var entete, lignes []string
	selLigne := 0
	entete = append(entete, styleTitre.Render("🎙 claude-voix › Voix")+"   "+styleDiscret.Render("la file des réponses est en pause"), "")
	if m.ev.err != "" {
		entete = append(entete, styleErreur.Render("  "+m.ev.err), "")
	}
	if len(m.ev.voix) == 0 && m.ev.err == "" {
		entete = append(entete, styleDiscret.Render("  Chargement des voix…"))
	}
	actuelle := lireConfig().KyutaiVoix
	section, genre := "", ""
	for i, v := range m.ev.voix {
		s := "Menu"
		if v.Autre {
			s = "Autres voix, à tester (dis à Claude lesquelles ajouter au menu)"
		}
		if s != section {
			if section != "" {
				lignes = append(lignes, "")
			}
			section, genre = s, ""
			lignes = append(lignes, styleTitre.Render("  "+section))
		}
		if v.Genre != genre {
			genre = v.Genre
			lignes = append(lignes, "  "+styleNom.Render(strings.ToUpper(genre[:1])+genre[1:]+"s"))
		}
		curseur := "  "
		if i == m.ev.sel {
			curseur = styleTitre.Render("▸ ")
			selLigne = len(lignes)
		}
		ligne := fmt.Sprintf("    %s%-12s %s", curseur, v.Nom, styleDiscret.Render(fmt.Sprintf("%-16s", v.Description)))
		var etat []string
		if v.Nom == actuelle {
			etat = append(etat, styleLecture.Render("✓ lit tes réponses"))
		}
		switch {
		case m.ev.enCours == v.Nom:
			etat = append(etat, styleAttente.Render("génération…"))
		case m.essai != nil && i == m.ev.sel:
			etat = append(etat, styleLecture.Render("▶ écoute"))
		}
		lignes = append(lignes, ligne+"  "+strings.Join(etat, "  "))
	}
	pied := []string{
		"",
		styleDiscret.Render("  Phrase d'essai : « " + phraseEssai("[nom]") + " »"),
		"",
		styleAide.Width(l).Render("entrée écouter · espace choisir pour lire les réponses · ↑↓ parcourir · échap retour · q quitter"),
	}
	// Défilement : la voix choisie reste visible.
	place := max(m.hauteur-len(entete)-len(pied)-1, 5)
	debut := 0
	if len(lignes) > place {
		debut = min(max(selLigne-place/2, 0), len(lignes)-place)
		lignes = lignes[debut : debut+place]
	}
	tout := append(append(entete, lignes...), pied...)
	return lipgloss.NewStyle().MaxWidth(l).Render(strings.Join(tout, "\n")) + "\n"
}
