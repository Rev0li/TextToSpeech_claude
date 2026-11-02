// claude-voix : gestionnaire de conversation vocale pour Claude Code.
//
//	claude-voix                                   ouvre le gestionnaire (lecture des réponses)
//	claude-voix envoyer --session ID --cwd DIR    texte sur l'entrée standard (utilisé par le plugin tts)
//	claude-voix couper [--session ID]             coupe une session, ou tout
//
// Sans gestionnaire ouvert, « envoyer » échoue en silence : rien n'est lu.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "envoyer":
			os.Exit(commandeEnvoyer(os.Args[2:]))
		case "couper":
			os.Exit(commandeCouper(os.Args[2:]))
		case "-h", "--help", "aide":
			fmt.Println("Usage : claude-voix | claude-voix envoyer --session ID --cwd DIR < texte | claude-voix couper [--session ID]")
			return
		default:
			fmt.Fprintf(os.Stderr, "Commande inconnue : %s (voir claude-voix aide)\n", os.Args[1])
			os.Exit(2)
		}
	}
	if err := gestionnaire(); err != nil {
		fmt.Fprintln(os.Stderr, "claude-voix :", err)
		os.Exit(1)
	}
}

func commandeEnvoyer(args []string) int {
	fs := flag.NewFlagSet("envoyer", flag.ContinueOnError)
	session := fs.String("session", "", "identifiant de la session Claude Code")
	cwd := fs.String("cwd", "", "dossier de la session")
	if fs.Parse(args) != nil {
		return 2
	}
	texte, err := io.ReadAll(io.LimitReader(os.Stdin, tailleMaxMessage/2))
	if err != nil || len(texte) == 0 {
		return 1
	}
	if envoyer(cheminSocket(), Message{Type: "reponse", Session: *session, Cwd: *cwd, Texte: string(texte)}) != nil {
		return 1 // gestionnaire fermé : rien n'est lu
	}
	return 0
}

func commandeCouper(args []string) int {
	fs := flag.NewFlagSet("couper", flag.ContinueOnError)
	session := fs.String("session", "", "session à couper (toutes si vide)")
	if fs.Parse(args) != nil {
		return 2
	}
	if envoyer(cheminSocket(), Message{Type: "couper", Session: *session}) != nil {
		return 1
	}
	return 0
}

func gestionnaire() error {
	dossierAudio, err := os.MkdirTemp(dossierExecution(), "claude-voix-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dossierAudio)

	// Couleurs fixes : inutile d'interroger le terminal sur sa couleur de fond
	// (certains terminaux ne répondent pas et le démarrage attend plusieurs secondes).
	lipgloss.SetHasDarkBackground(true)

	travaux := make(chan travail, 100)
	m := nouveauModele(chargerNoms(), dossierAudio, travaux)
	p := tea.NewProgram(m, tea.WithAltScreen())

	chemin := cheminSocket()
	l, err := ecouter(chemin, p)
	if err != nil {
		return err
	}
	defer func() {
		l.Close()
		_ = os.Remove(chemin)
	}()

	cfg := lireConfig()
	lance := demarrerKyutai(cfg)
	go generateur(travaux, p)
	_, err = p.Run()
	m.Quitter()
	close(travaux)
	if lance {
		fmt.Println("Arrêt du serveur Kyutai (libère la carte graphique)…")
		arreterKyutai(cfg)
	}
	return err
}
