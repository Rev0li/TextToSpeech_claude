package main

import (
	"context"
	"fmt"
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
	tp       *tampon // audio de la réponse, rempli pendant la génération
	moteur   string
	err      string
	debutGen time.Time
	annuler  context.CancelFunc
}

type tick time.Time
type etatKyutai struct{ pret bool }

type modele struct {
	cartes        []*carte
	sel           int
	suivre        bool
	noms          *Noms
	travaux       chan<- travail
	cps           float64 // caractères dits par seconde, appris au fil des réponses
	lecture       *lecture
	pause         bool
	renommage     bool
	saisie        textinput.Model
	largeur       int
	hauteur       int
	kyutai        bool
	kyutaiDebut   time.Time // lancement du serveur par claude-voix
	annonce       string    // bandeau temporaire (Kyutai en ligne, arrêté…)
	annonceJusqua time.Time
	ev            ecranVoix // écran « Voix » (touche v)
	essai         *lecture  // lecture d\'un essai de voix
	ticks         int
	prochainID    int
}

func nouveauModele(noms *Noms, travaux chan<- travail) *modele {
	saisie := textinput.New()
	saisie.Prompt = "Nouveau nom : "
	saisie.CharLimit = 30
	return &modele{
		noms: noms, travaux: travaux, cps: 15,
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

// liberer arrête tout ce qui concerne la réponse actuelle d'une carte (génération, lecture).
// La génération annulée libère aussi le serveur Kyutai pour la réponse suivante.
func (m *modele) liberer(c *carte) {
	if c.annuler != nil {
		c.annuler()
	}
	if c.tp != nil {
		c.tp.abandonner()
	}
	if m.lecture != nil && m.lecture.id == c.id {
		m.lecture.arreter()
		m.lecture = nil
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
	c.etat, c.err, c.moteur, c.annuler, c.tp = enAttente, "", "", annuler, nouveauTampon()
	select {
	case m.travaux <- travail{id: c.id, texte: texte, tp: c.tp, ctx: ctx}:
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

// lancerSuivant joue la carte en cours la plus ancienne dès que possible : génération finie,
// ou assez avancée pour que la lecture ne la rattrape pas (voir peutDemarrer).
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
	if premiere == nil {
		return nil
	}
	if premiere.etat == pret || (premiere.etat == enGeneration && peutDemarrer(premiere.tp, premiere.texte, premiere.debutGen, m.cps)) {
		return m.jouer(premiere)
	}
	return nil // on attend : l'ordre d'arrivée est respecté
}

func (m *modele) jouer(c *carte) tea.Cmd {
	l, attendre, err := demarrerLecture(c.id, c.tp, lireConfig().Volume)
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

func (m *modele) annoncer(texte string) {
	m.annonce, m.annonceJusqua = texte, time.Now().Add(10*time.Second)
}

// basculerKyutai (touche k) : arrête le serveur s'il tourne, sinon le démarre.
func (m *modele) basculerKyutai() tea.Cmd {
	cfg := lireConfig()
	if cfg.KyutaiScript == "" {
		m.annoncer("Kyutai n'est pas installé ici (./install.sh --kyutai)")
		return nil
	}
	if m.kyutai || kyutaiDemarre.Load() {
		kyutaiDemarre.Store(false)
		m.annoncer("Arrêt de Kyutai…")
		return func() tea.Msg {
			arreterKyutai(cfg)
			return verifierKyutai()
		}
	}
	if demarrerKyutai(cfg) {
		m.kyutaiDebut = time.Now()
		m.annoncer("Démarrage de Kyutai…")
	}
	return nil
}

func (m *modele) Quitter() {
	if m.lecture != nil {
		m.lecture.arreter()
	}
	if m.essai != nil {
		m.essai.arreter()
	}
	for _, c := range m.cartes {
		if c.annuler != nil {
			c.annuler()
		}
	}
}

// ---- mise à jour -----------------------------------------------------------

func (m *modele) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if cmd, traite := m.majVoix(msg); traite {
		return m, cmd
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.largeur, m.hauteur = msg.Width, msg.Height
		return m, nil

	case tick:
		m.ticks++
		cmds := []tea.Cmd{prochainTick()}
		if m.ticks%6 == 0 || (kyutaiDemarre.Load() && m.ticks%2 == 0) {
			cmds = append(cmds, verifierKyutai)
		}
		cmds = append(cmds, m.lancerSuivant()) // démarrage anticipé pendant la génération
		return m, tea.Batch(cmds...)

	case etatKyutai:
		switch {
		case msg.pret && !m.kyutai:
			texte := "✓ Kyutai est en ligne"
			if !m.kyutaiDebut.IsZero() {
				texte += fmt.Sprintf(" (prêt en %d s)", int(time.Since(m.kyutaiDebut).Seconds()))
			}
			m.annoncer(texte + " : les réponses seront lues avec la voix " + lireConfig().KyutaiVoix)
		case !msg.pret && m.kyutai:
			m.annoncer("✗ Kyutai est arrêté : la voix Piper prend le relais (touche k pour le relancer)")
		}
		if msg.pret {
			kyutaiDemarre.Store(false)
			m.kyutaiDebut = time.Time{}
		}
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
		if c == nil || c.etat == coupe { // réponse remplacée, carte supprimée ou coupée
			return m, m.lancerSuivant()
		}
		c.moteur = msg.moteur
		audio, _, _ := c.tp.etat()
		switch {
		case msg.err != nil && c.etat != enLecture:
			c.etat, c.err = echec, msg.err.Error()
		case msg.err != nil:
			c.err = msg.err.Error() // la lecture joue ce qui a été produit
		default:
			if c.etat == enGeneration {
				c.etat = pret
			}
			if msg.moteur == "kyutai" && audio > 2*time.Second {
				m.cps = 0.7*m.cps + 0.3*float64(len([]rune(c.texte)))/audio.Seconds()
			}
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
		if m.ev.ouvert {
			return m.toucheVoix(msg)
		}
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
	case "left":
		m.deplacer(-1)
	case "right":
		m.deplacer(1)
	case "up":
		m.deplacer(-m.colonnes())
	case "down":
		m.deplacer(m.colonnes())
	case "esc":
		m.suivre = true
		if m.lecture != nil {
			if c := m.parID(m.lecture.id); c != nil {
				m.sel = m.index(c)
			}
		}
	case "enter":
		if c := m.selection(); c != nil && c.tp != nil && (c.etat == lu || c.etat == passe) {
			if audio, fini, _ := c.tp.etat(); fini && audio > 0 {
				m.arreterLecture(passe)
				m.pause = false
				return m, m.jouer(c)
			}
		}
	case "v":
		return m, m.ouvrirVoix()
	case "k":
		return m, m.basculerKyutai()
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

	styleTitre         = lipgloss.NewStyle().Bold(true).Foreground(couleurSel)
	styleDiscret       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleNom           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styleLecture       = lipgloss.NewStyle().Foreground(couleurLecture).Bold(true)
	styleAttente       = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleErreur        = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleAide          = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleCarte         = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	styleAnnonce       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(couleurLecture).Padding(0, 1)
	styleAnnonceErreur = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160")).Padding(0, 1)
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
		audio, _, _ := c.tp.etat()
		return styleAttente.Render(fmt.Sprintf("génération %ds · %s", int(time.Since(c.debutGen).Seconds()), duree(audio)))
	case pret:
		audio, _, _ := c.tp.etat()
		return styleAttente.Render("prêt " + duree(audio))
	case enLecture:
		if m.lecture != nil && m.lecture.id == c.id {
			icone := "▶"
			if m.pause {
				icone = "⏸"
			}
			audio, fini, _ := c.tp.etat()
			total := duree(audio)
			if !fini {
				total += "…"
			}
			return styleLecture.Render(fmt.Sprintf("%s %s / %s", icone, duree(m.lecture.ecoule()), total))
		}
		return styleLecture.Render("lecture")
	case lu:
		audio, _, _ := c.tp.etat()
		return styleDiscret.Render("lu " + duree(audio))
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
	if m.ev.ouvert {
		return m.vueVoix(l)
	}
	var b strings.Builder

	moteur := styleErreur.Render("Kyutai arrêté → Piper")
	if kyutaiDemarre.Load() && !m.kyutai {
		moteur = styleAttente.Render("Kyutai démarre…")
		if !m.kyutaiDebut.IsZero() {
			moteur = styleAttente.Render(fmt.Sprintf("Kyutai démarre… %d s", int(time.Since(m.kyutaiDebut).Seconds())))
		}
	}
	if m.kyutai {
		moteur = styleLecture.Render("Kyutai prêt") + styleDiscret.Render(" ("+lireConfig().KyutaiVoix+")")
	}
	etatGlobal := ""
	if m.pause {
		etatGlobal = styleAttente.Render("  ⏸ en pause")
	}
	b.WriteString(styleTitre.Render("🎙 claude-voix") + "   " + moteur + etatGlobal + "\n")
	if m.annonce != "" && time.Now().Before(m.annonceJusqua) {
		style := styleAnnonce
		if strings.HasPrefix(m.annonce, "✗") {
			style = styleAnnonceErreur
		}
		b.WriteString(style.Width(l).Render(m.annonce) + "\n")
	}

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
		aide := "espace pause · s suivant · c tout couper · d supprimer · entrée relire · r renommer · v voix · k Kyutai · q quitter\n" +
			"flèches choisir une carte · échap suivre la lecture"
		b.WriteString(styleAide.Width(l).Render(aide) + "\n")
	}
	return b.String()
}
