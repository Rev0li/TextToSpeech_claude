package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Une carte par fenêtre Claude Code (session). Une nouvelle réponse de la même fenêtre
// remplace l'ancienne dans sa carte : celle-ci est coupée si elle était en attente ou en
// lecture. Les cartes sont lues dans l'ordre d'arrivée de leur réponse.

type etat int

const (
	enAttente etat = iota
	enGeneration
	pret
	enLecture
	lu
	passe
	coupe
	echec
)

func (e etat) enCours() bool { return e == enAttente || e == enGeneration || e == pret }

const (
	largeurCarteMin = 46
	colonnesMax     = 3
	lignesCarte     = 4 // lignes de texte d'une carte
	lignesCarteSel  = 9 // lignes de texte de la carte choisie
)

type carte struct {
	id       int // change à chaque nouvelle réponse
	session  string
	cwd      string
	texte    string
	recu     time.Time
	etat     etat
	chemin   string
	duree    time.Duration
	moteur   string
	err      string
	debutGen time.Time
	annuler  context.CancelFunc
}

type tick time.Time
type etatKyutai struct{ pret bool }

type modele struct {
	cartes       []*carte
	sel          int
	suivre       bool
	noms         *Noms
	dossierAudio string
	travaux      chan<- travail
	lecture      *lecture
	pause        bool
	renommage    bool
	saisie       textinput.Model
	largeur      int
	hauteur      int
	kyutai       bool
	ticks        int
	prochainID   int
}

func nouveauModele(noms *Noms, dossierAudio string, travaux chan<- travail) *modele {
	saisie := textinput.New()
	saisie.Prompt = "Nouveau nom : "
	saisie.CharLimit = 30
	return &modele{
		noms: noms, dossierAudio: dossierAudio, travaux: travaux,
		saisie: saisie, suivre: true, largeur: 80, hauteur: 24,
		kyutai: kyutaiPret(lireConfig()),
	}
}

func prochainTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tick(t) })
}

func verifierKyutai() tea.Msg { return etatKyutai{kyutaiPret(lireConfig())} }

func (m *modele) Init() tea.Cmd { return prochainTick() }

// ---- logique ---------------------------------------------------------------

func (m *modele) parID(id int) *carte {
	for _, c := range m.cartes {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (m *modele) index(c *carte) int {
	for i, x := range m.cartes {
		if x == c {
			return i
		}
	}
	return -1
}

// liberer arrête tout ce qui concerne la réponse actuelle d'une carte (génération, lecture, fichier).
func (m *modele) liberer(c *carte) {
	if c.annuler != nil {
		c.annuler()
	}
	if m.lecture != nil && m.lecture.id == c.id {
		m.lecture.arreter()
		m.lecture = nil
	}
	if c.chemin != "" {
		_ = os.Remove(c.chemin)
	}
}

func (m *modele) recevoir(msg messageRecu) {
	texte := strings.TrimSpace(msg.Texte)
	if texte == "" {
		return
	}
	var c *carte
	for _, x := range m.cartes {
		if x.session == msg.Session {
			c = x
			break
		}
	}
	if c == nil {
		c = &carte{session: msg.Session}
		m.cartes = append(m.cartes, c)
	} else {
		m.liberer(c) // la nouvelle réponse écrase l'ancienne
	}
	m.prochainID++
	ctx, annuler := context.WithCancel(context.Background())
	c.id, c.cwd, c.texte, c.recu = m.prochainID, msg.Cwd, texte, time.Now()
	c.etat, c.err, c.moteur, c.duree, c.annuler = enAttente, "", "", 0, annuler
	c.chemin = filepath.Join(m.dossierAudio, fmt.Sprintf("%d.wav", c.id))
	select {
	case m.travaux <- travail{id: c.id, texte: texte, chemin: c.chemin, ctx: ctx}:
	default:
		c.etat, c.err = echec, "file de génération pleine"
	}
	if m.suivre && m.lecture == nil {
		m.sel = m.index(c)
	}
}

// couper arrête les cartes d'une session (toutes si session est vide), en cours ou à venir.
func (m *modele) couper(session string) {
	for _, c := range m.cartes {
		if session != "" && c.session != session {
			continue
		}
		if c.etat.enCours() || c.etat == enLecture {
			if c.annuler != nil {
				c.annuler()
			}
			if m.lecture != nil && m.lecture.id == c.id {
				m.lecture.arreter()
				m.lecture = nil
			}
			c.etat = coupe
		}
	}
}

// supprimer retire la carte choisie (sa lecture ou sa génération est arrêtée).
func (m *modele) supprimer() {
	c := m.selection()
	if c == nil {
		return
	}
	m.liberer(c)
	i := m.index(c)
	m.cartes = append(m.cartes[:i], m.cartes[i+1:]...)
	if m.sel >= len(m.cartes) {
		m.sel = max(len(m.cartes)-1, 0)
	}
}

// lancerSuivant joue la carte en cours la plus ancienne, si elle est prête.
func (m *modele) lancerSuivant() tea.Cmd {
	if m.lecture != nil || m.pause {
		return nil
	}
	var premiere *carte
	for _, c := range m.cartes {
		if c.etat.enCours() && (premiere == nil || c.recu.Before(premiere.recu)) {
			premiere = c
		}
	}
	if premiere == nil || premiere.etat != pret {
		return nil // on attend sa génération : l'ordre d'arrivée est respecté
	}
	return m.jouer(premiere)
}

func (m *modele) jouer(c *carte) tea.Cmd {
	l, attendre, err := demarrerLecture(c.id, c.chemin, lireConfig().Volume)
	if err != nil {
		c.etat, c.err = echec, "paplay : "+err.Error()
		return nil
	}
	m.lecture = l
	c.etat = enLecture
	if m.suivre {
		m.sel = m.index(c)
	}
	return attendre
}

func (m *modele) arreterLecture(nouvel etat) {
	if m.lecture == nil {
		return
	}
	if c := m.parID(m.lecture.id); c != nil {
		c.etat = nouvel
	}
	m.lecture.arreter()
	m.lecture = nil
}

func (m *modele) basculerPause() tea.Cmd {
	m.pause = !m.pause
	if m.lecture != nil {
		if m.pause {
			m.lecture.pause()
		} else {
			m.lecture.reprendre()
		}
		return nil
	}
	return m.lancerSuivant()
}

func (m *modele) Quitter() {
	if m.lecture != nil {
		m.lecture.arreter()
	}
	for _, c := range m.cartes {
		if c.annuler != nil {
			c.annuler()
		}
	}
}

// ---- mise à jour -----------------------------------------------------------

func (m *modele) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.largeur, m.hauteur = msg.Width, msg.Height
		return m, nil

	case tick:
		m.ticks++
		cmds := []tea.Cmd{prochainTick()}
		if m.ticks%10 == 0 || (kyutaiDemarre.Load() && m.ticks%2 == 0) {
			cmds = append(cmds, verifierKyutai)
		}
		return m, tea.Batch(cmds...)

	case etatKyutai:
		m.kyutai = msg.pret
		return m, nil

	case messageRecu:
		switch msg.Type {
		case "reponse":
			m.recevoir(msg)
		case "couper":
			m.couper(msg.Session)
		}
		return m, m.lancerSuivant()

	case debutGeneration:
		if c := m.parID(msg.id); c != nil && c.etat == enAttente {
			c.etat, c.debutGen = enGeneration, time.Now()
		}
		return m, nil

	case genere:
		c := m.parID(msg.id)
		switch {
		case c == nil || c.etat == coupe: // réponse remplacée, carte supprimée ou coupée
			_ = os.Remove(msg.chemin)
		case msg.err != nil:
			c.etat, c.err = echec, msg.err.Error()
		default:
			c.etat, c.moteur, c.duree = pret, msg.moteur, msg.duree
		}
		return m, m.lancerSuivant()

	case finLecture:
		if m.lecture != nil && m.lecture.id == msg.id {
			m.lecture = nil
			if c := m.parID(msg.id); c != nil && c.etat == enLecture {
				c.etat = lu
			}
		}
		return m, m.lancerSuivant()

	case tea.KeyMsg:
		if m.renommage {
			return m.toucheRenommage(msg)
		}
		return m.touche(msg)
	}
	return m, nil
}

func (m *modele) colonnes() int {
	return min(max(m.largeur/largeurCarteMin, 1), colonnesMax)
}

func (m *modele) deplacer(pas int) {
	if len(m.cartes) == 0 {
		return
	}
	m.sel = min(max(m.sel+pas, 0), len(m.cartes)-1)
	m.suivre = false
}

func (m *modele) touche(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case " ", "p":
		return m, m.basculerPause()
	case "s":
		m.arreterLecture(passe)
		return m, m.lancerSuivant()
	case "c":
		m.couper("")
		return m, nil
	case "d", "delete", "backspace":
		m.supprimer()
		return m, m.lancerSuivant()
	case "left", "h":
		m.deplacer(-1)
	case "right", "l":
		m.deplacer(1)
	case "up", "k":
		m.deplacer(-m.colonnes())
	case "down", "j":
		m.deplacer(m.colonnes())
	case "esc":
		m.suivre = true
		if m.lecture != nil {
			if c := m.parID(m.lecture.id); c != nil {
				m.sel = m.index(c)
			}
		}
	case "enter":
		if c := m.selection(); c != nil && c.chemin != "" && (c.etat == lu || c.etat == passe || c.etat == coupe) {
			if _, err := os.Stat(c.chemin); err == nil {
				m.arreterLecture(passe)
				m.pause = false
				return m, m.jouer(c)
			}
		}
	case "r":
		if c := m.selection(); c != nil {
			m.renommage = true
			m.saisie.SetValue(m.noms.Nom(c.session, c.cwd))
			m.saisie.CursorEnd()
			return m, m.saisie.Focus()
		}
	}
	return m, nil
}

func (m *modele) toucheRenommage(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.renommage = false
		m.saisie.Blur()
		return m, nil
	case "enter":
		nom := strings.TrimSpace(m.saisie.Value())
		if c := m.selection(); c != nil && nom != "" {
			_ = m.noms.Renommer(c.session, c.cwd, nom)
		}
		m.renommage = false
		m.saisie.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.saisie, cmd = m.saisie.Update(msg)
	return m, cmd
}

func (m *modele) selection() *carte {
	if m.sel < 0 || m.sel >= len(m.cartes) {
		return nil
	}
	return m.cartes[m.sel]
}

// ---- affichage -------------------------------------------------------------

var (
	couleurSel     = lipgloss.Color("212")
	couleurLecture = lipgloss.Color("42")
	couleurCadre   = lipgloss.Color("240")

	styleTitre   = lipgloss.NewStyle().Bold(true).Foreground(couleurSel)
	styleDiscret = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleNom     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleLecture = lipgloss.NewStyle().Foreground(couleurLecture).Bold(true)
	styleAttente = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErreur  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleAide    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleCarte   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

func duree(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (m *modele) etiquette(c *carte) string {
	switch c.etat {
	case enAttente:
		return styleAttente.Render("en attente")
	case enGeneration:
		return styleAttente.Render(fmt.Sprintf("génération %ds", int(time.Since(c.debutGen).Seconds())))
	case pret:
		return styleAttente.Render("prêt " + duree(c.duree))
	case enLecture:
		if m.lecture != nil && m.lecture.id == c.id {
			icone := "▶"
			if m.pause {
				icone = "⏸"
			}
			return styleLecture.Render(fmt.Sprintf("%s %s / %s", icone, duree(m.lecture.ecoule()), duree(c.duree)))
		}
		return styleLecture.Render("lecture")
	case lu:
		return styleDiscret.Render("lu " + duree(c.duree))
	case passe:
		return styleDiscret.Render("passé")
	case coupe:
		return styleDiscret.Render("coupé")
	case echec:
		return styleErreur.Render("échec")
	}
	return ""
}

func (m *modele) rendreCarte(c *carte, largeur int, choisie bool) string {
	interieur := largeur - 4 // bordure + marge
	nom := styleNom.Render(runewidth.Truncate(m.noms.Nom(c.session, c.cwd), interieur/2, "…"))
	heure := styleDiscret.Render(c.recu.Format("15:04"))
	etiq := m.etiquette(c)
	espace := max(interieur-lipgloss.Width(nom)-lipgloss.Width(etiq)-lipgloss.Width(heure)-2, 1)
	entete := nom + " " + etiq + strings.Repeat(" ", espace) + " " + heure

	texte := c.texte
	if c.err != "" {
		texte = styleErreur.Render(c.err) + "\n" + texte
	}
	limite := lignesCarte
	if choisie {
		limite = lignesCarteSel
	}
	lignes := strings.Split(lipgloss.NewStyle().Width(interieur).Render(texte), "\n")
	if len(lignes) > limite {
		lignes = append(lignes[:limite-1], "…")
	}
	for len(lignes) < lignesCarte {
		lignes = append(lignes, "")
	}
	corps := strings.Join(lignes, "\n")
	if !choisie {
		corps = styleDiscret.Render(corps)
	}

	bord := couleurCadre
	switch {
	case choisie:
		bord = couleurSel
	case c.etat == enLecture:
		bord = couleurLecture
	}
	return styleCarte.BorderForeground(bord).Width(largeur - 2).Render(entete + "\n" + corps)
}

func (m *modele) View() string {
	l := max(m.largeur, largeurCarteMin)
	var b strings.Builder

	moteur := styleErreur.Render("Kyutai arrêté → Piper")
	if kyutaiDemarre.Load() && !m.kyutai {
		moteur = styleAttente.Render("Kyutai démarre…")
	}
	if m.kyutai {
		moteur = styleLecture.Render("Kyutai prêt") + styleDiscret.Render(" ("+lireConfig().KyutaiVoix+")")
	}
	etatGlobal := ""
	if m.pause {
		etatGlobal = styleAttente.Render("  ⏸ en pause")
	}
	b.WriteString(styleTitre.Render("🎙 claude-voix") + "   " + moteur + etatGlobal + "\n")

	if len(m.cartes) == 0 {
		b.WriteString("\n" + styleDiscret.Render("  En attente des réponses de Claude Code…") + "\n")
	} else {
		cols := m.colonnes()
		largeur := l / cols
		var rangees []string
		for debut := 0; debut < len(m.cartes); debut += cols {
			var rangee []string
			for i := debut; i < min(debut+cols, len(m.cartes)); i++ {
				rangee = append(rangee, m.rendreCarte(m.cartes[i], largeur, i == m.sel))
			}
			rangees = append(rangees, lipgloss.JoinHorizontal(lipgloss.Top, rangee...))
		}
		// Défilement : la rangée de la carte choisie reste visible.
		dispo := max(m.hauteur-4, 6)
		rangSel := min(m.sel/cols, len(rangees)-1)
		debut := 0
		for debut < rangSel && lipgloss.Height(strings.Join(rangees[debut:rangSel+1], "\n")) > dispo {
			debut++
		}
		var visibles []string
		for i := debut; i < len(rangees); i++ {
			essai := append(append([]string{}, visibles...), rangees[i])
			if len(visibles) > 0 && lipgloss.Height(strings.Join(essai, "\n")) > dispo {
				break
			}
			visibles = essai
		}
		b.WriteString(strings.Join(visibles, "\n") + "\n")
	}

	if m.renommage {
		b.WriteString(m.saisie.View() + styleAide.Render("   entrée valider · échap annuler") + "\n")
	} else {
		aide := "espace pause · s suivant · c tout couper · d supprimer · entrée relire · r renommer · q quitter\n" +
			"flèches choisir une carte · échap suivre la lecture"
		b.WriteString(styleAide.Width(l).Render(aide) + "\n")
	}
	return b.String()
}
