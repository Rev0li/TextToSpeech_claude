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
	historiqueMax    = 200
	largeurEtiquette = 16
)

type element struct {
	id       int
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
	elements     []*element
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

func (m *modele) trouver(id int) *element {
	for _, e := range m.elements {
		if e.id == id {
			return e
		}
	}
	return nil
}

func (m *modele) ajouter(msg messageRecu) {
	texte := strings.TrimSpace(msg.Texte)
	if texte == "" {
		return
	}
	m.prochainID++
	ctx, annuler := context.WithCancel(context.Background())
	e := &element{
		id: m.prochainID, session: msg.Session, cwd: msg.Cwd, texte: texte,
		recu: time.Now(), etat: enAttente, annuler: annuler,
		chemin: filepath.Join(m.dossierAudio, fmt.Sprintf("%d.wav", m.prochainID)),
	}
	m.elements = append(m.elements, e)
	m.elaguer()
	select {
	case m.travaux <- travail{id: e.id, texte: texte, chemin: e.chemin, ctx: ctx}:
	default:
		e.etat, e.err = echec, "file de génération pleine"
	}
	if m.suivre && m.lecture == nil {
		m.sel = len(m.elements) - 1
	}
}

// elaguer garde l'historique sous historiqueMax en retirant les plus anciens éléments terminés.
func (m *modele) elaguer() {
	for len(m.elements) > historiqueMax {
		i := -1
		for j, e := range m.elements {
			if !e.etat.enCours() && e.etat != enLecture {
				i = j
				break
			}
		}
		if i < 0 {
			return
		}
		_ = os.Remove(m.elements[i].chemin)
		m.elements = append(m.elements[:i], m.elements[i+1:]...)
		if m.sel > i {
			m.sel--
		}
	}
}

// couper arrête les lectures d'une session (toutes si session est vide), en cours ou à venir.
func (m *modele) couper(session string) {
	for _, e := range m.elements {
		if session != "" && e.session != session {
			continue
		}
		if e.etat.enCours() {
			e.annuler()
			e.etat = coupe
		}
		if e.etat == enLecture && m.lecture != nil && m.lecture.id == e.id {
			m.lecture.arreter()
			m.lecture = nil
			e.etat = coupe
		}
	}
}

// lancerSuivant joue le premier élément en cours, dans l'ordre d'arrivée, s'il est prêt.
func (m *modele) lancerSuivant() tea.Cmd {
	if m.lecture != nil || m.pause {
		return nil
	}
	for i, e := range m.elements {
		if !e.etat.enCours() {
			continue
		}
		if e.etat != pret {
			return nil // on attend sa génération : l'ordre d'arrivée est respecté
		}
		return m.jouer(i)
	}
	return nil
}

func (m *modele) jouer(i int) tea.Cmd {
	e := m.elements[i]
	l, attendre, err := demarrerLecture(e.id, e.chemin, lireConfig().Volume)
	if err != nil {
		e.etat, e.err = echec, "paplay : "+err.Error()
		return nil
	}
	m.lecture = l
	e.etat = enLecture
	if m.suivre {
		m.sel = i
	}
	return attendre
}

// arreterLecture stoppe la lecture en cours et marque l'élément avec l'état donné.
func (m *modele) arreterLecture(nouvel etat) {
	if m.lecture == nil {
		return
	}
	if e := m.trouver(m.lecture.id); e != nil {
		e.etat = nouvel
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
	for _, e := range m.elements {
		if e.annuler != nil {
			e.annuler()
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
			m.ajouter(msg)
		case "couper":
			m.couper(msg.Session)
		}
		return m, m.lancerSuivant()

	case debutGeneration:
		if e := m.trouver(msg.id); e != nil && e.etat == enAttente {
			e.etat, e.debutGen = enGeneration, time.Now()
		}
		return m, nil

	case genere:
		e := m.trouver(msg.id)
		if e == nil {
			return m, nil
		}
		switch {
		case e.etat == coupe:
			_ = os.Remove(e.chemin)
		case msg.err != nil:
			e.etat, e.err = echec, msg.err.Error()
		default:
			e.etat, e.moteur, e.duree = pret, msg.moteur, msg.duree
		}
		return m, m.lancerSuivant()

	case finLecture:
		if m.lecture != nil && m.lecture.id == msg.id {
			m.lecture = nil
			if e := m.trouver(msg.id); e != nil && e.etat == enLecture {
				e.etat = lu
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

func (m *modele) touche(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case " ", "p":
		return m, m.basculerPause()
	case "s", "right":
		m.arreterLecture(passe)
		return m, m.lancerSuivant()
	case "c":
		m.couper("")
		return m, nil
	case "up", "k":
		if m.sel > 0 {
			m.sel--
		}
		m.suivre = false
	case "down", "j":
		if m.sel < len(m.elements)-1 {
			m.sel++
		}
		m.suivre = false
	case "esc":
		m.suivre = true
		if m.lecture != nil {
			for i, e := range m.elements {
				if e.id == m.lecture.id {
					m.sel = i
				}
			}
		} else if len(m.elements) > 0 {
			m.sel = len(m.elements) - 1
		}
	case "enter":
		if e := m.selection(); e != nil && e.chemin != "" && (e.etat == lu || e.etat == passe || e.etat == coupe) {
			if _, err := os.Stat(e.chemin); err == nil {
				m.arreterLecture(passe)
				m.pause = false
				return m, m.jouer(m.sel)
			}
		}
	case "r":
		if e := m.selection(); e != nil {
			m.renommage = true
			m.saisie.SetValue(m.noms.Nom(e.session, e.cwd))
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
		if e := m.selection(); e != nil && nom != "" {
			_ = m.noms.Renommer(e.session, e.cwd, nom)
		}
		m.renommage = false
		m.saisie.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.saisie, cmd = m.saisie.Update(msg)
	return m, cmd
}

func (m *modele) selection() *element {
	if m.sel < 0 || m.sel >= len(m.elements) {
		return nil
	}
	return m.elements[m.sel]
}

// ---- affichage -------------------------------------------------------------

var (
	styleTitre   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleDiscret = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleNom     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleLecture = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	styleAttente = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErreur  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleSel     = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	styleCadre   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	styleAide    = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
)

func duree(d time.Duration) string {
	s := int(d.Round(time.Second).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (m *modele) etiquette(e *element) string {
	switch e.etat {
	case enAttente:
		return styleAttente.Render("en attente")
	case enGeneration:
		return styleAttente.Render(fmt.Sprintf("génération %ds", int(time.Since(e.debutGen).Seconds())))
	case pret:
		return styleAttente.Render("prêt " + duree(e.duree))
	case enLecture:
		if m.lecture != nil && m.lecture.id == e.id {
			icone := "▶"
			if m.pause {
				icone = "⏸"
			}
			return styleLecture.Render(fmt.Sprintf("%s %s / %s", icone, duree(m.lecture.ecoule()), duree(e.duree)))
		}
		return styleLecture.Render("lecture")
	case lu:
		return styleDiscret.Render("lu " + duree(e.duree))
	case passe:
		return styleDiscret.Render("passé")
	case coupe:
		return styleDiscret.Render("coupé")
	case echec:
		return styleErreur.Render("échec")
	}
	return ""
}

func (m *modele) View() string {
	l := max(m.largeur, 40)
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
	b.WriteString(styleTitre.Render("🎙 claude-voix") + "   " + moteur + etatGlobal + "\n\n")

	// Liste : on garde de la place pour le détail et l'aide.
	place := max(m.hauteur-14, 3)
	debut := 0
	if len(m.elements) > place {
		debut = min(max(m.sel-place/2, 0), len(m.elements)-place)
	}
	if len(m.elements) == 0 {
		b.WriteString(styleDiscret.Render("  En attente des réponses de Claude Code…") + "\n")
	}
	for i := debut; i < len(m.elements) && i < debut+place; i++ {
		e := m.elements[i]
		nom := runewidth.Truncate(m.noms.Nom(e.session, e.cwd), 12, "…")
		nom = styleNom.Render(runewidth.FillRight(nom, 12))
		etiq := m.etiquette(e)
		etiq += strings.Repeat(" ", max(largeurEtiquette-lipgloss.Width(etiq), 0))
		reste := l - 12 - largeurEtiquette - 13
		apercu := strings.Join(strings.Fields(e.texte), " ")
		apercu = styleDiscret.Render(runewidth.Truncate(apercu, max(reste, 5), "…"))
		ligne := fmt.Sprintf(" %s  %s  %s  %s", e.recu.Format("15:04"), nom, etiq, apercu)
		if i == m.sel {
			ligne = styleSel.Render(runewidth.FillRight(ligne, l-1))
		}
		b.WriteString(ligne + "\n")
	}

	// Détail de l'élément sélectionné.
	if e := m.selection(); e != nil {
		texte := e.texte
		if e.err != "" {
			texte = styleErreur.Render(e.err) + "\n\n" + texte
		}
		lignes := strings.Split(lipgloss.NewStyle().Width(l-6).Render(texte), "\n")
		if len(lignes) > 6 {
			lignes = append(lignes[:6], styleDiscret.Render("…"))
		}
		titre := styleNom.Render(m.noms.Nom(e.session, e.cwd))
		if e.moteur != "" {
			titre += styleDiscret.Render(" · " + e.moteur)
		}
		b.WriteString("\n" + styleCadre.Width(l-2).Render(titre+"\n"+strings.Join(lignes, "\n")) + "\n")
	}

	if m.renommage {
		b.WriteString("\n" + m.saisie.View() + styleAide.Render("   entrée valider · échap annuler") + "\n")
	} else {
		aide := "espace pause · s suivant · c tout couper · entrée relire · r renommer · q quitter\n" +
			"↑↓ parcourir · échap suivre la lecture"
		b.WriteString("\n" + styleAide.Width(l).Render(aide) + "\n")
	}
	return b.String()
}
