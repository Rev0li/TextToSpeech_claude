package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- génération ----------------------------------------------------------

type travail struct {
	id     int
	texte  string
	chemin string
	ctx    context.Context
}

type debutGeneration struct{ id int }

type genere struct {
	id     int
	moteur string
	duree  time.Duration
	err    error
}

// kyutaiDemarre : vrai pendant que le gestionnaire attend le serveur qu'il vient de lancer.
var kyutaiDemarre atomic.Bool

// generateur traite les travaux un par un, dans l'ordre d'arrivée.
func generateur(travaux <-chan travail, p *tea.Program) {
	for t := range travaux {
		if t.ctx.Err() != nil {
			continue
		}
		attendreKyutai(t.ctx)
		p.Send(debutGeneration{t.id})
		moteur, err := generer(t.ctx, lireConfig(), t.texte, t.chemin)
		p.Send(genere{id: t.id, moteur: moteur, duree: dureeWav(t.chemin), err: err})
	}
}

// generer : Kyutai si le serveur répond, sinon Piper.
func generer(ctx context.Context, cfg Config, texte, chemin string) (string, error) {
	if kyutaiPret(cfg) {
		err := genererKyutai(ctx, cfg, texte, chemin)
		if err == nil {
			return "kyutai", nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "piper", genererPiper(ctx, cfg, texte, chemin)
}

// attendreKyutai : si le serveur est en train de démarrer, l'attendre (2 min au plus)
// plutôt que de retomber tout de suite sur Piper.
func attendreKyutai(ctx context.Context) {
	limite := time.Now().Add(2 * time.Minute)
	for kyutaiDemarre.Load() && time.Now().Before(limite) && ctx.Err() == nil {
		if kyutaiPret(lireConfig()) {
			kyutaiDemarre.Store(false)
			return
		}
		time.Sleep(time.Second)
	}
	kyutaiDemarre.Store(false)
}

// demarrerKyutai lance « kyutai.sh start » en arrière-plan si le serveur ne répond pas.
// Rend vrai si c'est le gestionnaire qui l'a lancé (il l'arrêtera en quittant).
func demarrerKyutai(cfg Config) bool {
	if cfg.KyutaiScript == "" || kyutaiPret(cfg) {
		return false
	}
	if _, err := os.Stat(cfg.KyutaiScript); err != nil {
		return false
	}
	cmd := exec.Command(cfg.KyutaiScript, "start")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() != nil {
		return false
	}
	kyutaiDemarre.Store(true)
	go func() { _ = cmd.Wait() }()
	return true
}

func arreterKyutai(cfg Config) {
	_ = exec.Command(cfg.KyutaiScript, "stop").Run()
}

func kyutaiPret(cfg Config) bool {
	client := http.Client{Timeout: 2 * time.Second}
	r, err := client.Get(cfg.KyutaiURL + "/health")
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == http.StatusOK
}

func genererKyutai(ctx context.Context, cfg Config, texte, chemin string) error {
	corps, _ := json.Marshal(map[string]string{
		"input": texte, "voice": cfg.KyutaiVoix, "response_format": "wav",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.KyutaiURL+"/v1/audio/speech", bytes.NewReader(corps))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("Kyutai : HTTP %d", r.StatusCode)
	}
	f, err := os.OpenFile(chemin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func genererPiper(ctx context.Context, cfg Config, texte, chemin string) error {
	modele, err := voixPiper(cfg.VoixPiper)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "piper", "--model", modele, "--output_file", chemin)
	cmd.Stdin = strings.NewReader(texte)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("Piper : %v %s", err, lastLine(out))
	}
	return nil
}

// voixPiper : « fr_FR-siwis » → ~/.local/share/piper-voices/fr_FR-siwis-medium.onnx.
// Le locuteur (« fr_FR-upmc:pierre ») est ignoré : Piper prend le premier.
func voixPiper(nom string) (string, error) {
	nom, _, _ = strings.Cut(nom, ":")
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dossier := filepath.Join(home, ".local", "share", "piper-voices")
	for _, q := range []string{"medium", "high", "low", "x_low"} {
		p := filepath.Join(dossier, nom+"-"+q+".onnx")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("voix Piper « %s » introuvable dans %s", nom, dossier)
}

func lastLine(b []byte) string {
	lignes := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lignes[len(lignes)-1]
}

// dureeWav lit l'en-tête WAV (débit et taille des données) ; 0 si illisible.
func dureeWav(chemin string) time.Duration {
	data, err := os.ReadFile(chemin)
	if err != nil || len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0
	}
	var debit uint32
	for i := 12; i+8 <= len(data); {
		id := string(data[i : i+4])
		taille := int(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		debut := i + 8
		switch id {
		case "fmt ":
			if debut+12 <= len(data) {
				debit = binary.LittleEndian.Uint32(data[debut+8 : debut+12])
			}
		case "data":
			if taille <= 0 || debut+taille > len(data) {
				taille = len(data) - debut
			}
			if debit == 0 {
				return 0
			}
			return time.Duration(float64(taille) / float64(debit) * float64(time.Second))
		}
		i = debut + taille + taille%2
	}
	return 0
}

// ---- lecture -------------------------------------------------------------

type finLecture struct {
	id  int
	err error
}

type lecture struct {
	id          int
	cmd         *exec.Cmd
	debut       time.Time
	pauseDepuis time.Time
	pauseCumul  time.Duration
}

func demarrerLecture(id int, chemin, volume string) (*lecture, tea.Cmd, error) {
	cmd := exec.Command("paplay", "--volume="+volume, chemin)
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	l := &lecture{id: id, cmd: cmd, debut: time.Now()}
	attendre := func() tea.Msg { return finLecture{id: id, err: cmd.Wait()} }
	return l, attendre, nil
}

func (l *lecture) pause() {
	if l.pauseDepuis.IsZero() {
		_ = l.cmd.Process.Signal(syscall.SIGSTOP)
		l.pauseDepuis = time.Now()
	}
}

func (l *lecture) reprendre() {
	if !l.pauseDepuis.IsZero() {
		_ = l.cmd.Process.Signal(syscall.SIGCONT)
		l.pauseCumul += time.Since(l.pauseDepuis)
		l.pauseDepuis = time.Time{}
	}
}

func (l *lecture) arreter() {
	_ = l.cmd.Process.Signal(syscall.SIGCONT)
	_ = l.cmd.Process.Kill()
}

func (l *lecture) ecoule() time.Duration {
	fin := time.Now()
	if !l.pauseDepuis.IsZero() {
		fin = l.pauseDepuis
	}
	return fin.Sub(l.debut) - l.pauseCumul
}
