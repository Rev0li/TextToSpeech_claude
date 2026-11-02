package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ---- tampon audio --------------------------------------------------------

// tampon : PCM s16le mono d'une réponse, rempli au fil de la génération et lu par la
// lecture en même temps. Il reste en mémoire pour « relire ».
type tampon struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	taux   int // Hz
	fini   bool
	err    error
	annule bool
}

func nouveauTampon() *tampon {
	t := &tampon{}
	t.cond = sync.NewCond(&t.mu)
	return t
}

func (t *tampon) definirTaux(hz int) {
	t.mu.Lock()
	t.taux = hz
	t.mu.Unlock()
}

func (t *tampon) ecrire(b []byte) {
	t.mu.Lock()
	t.data = append(t.data, b...)
	t.mu.Unlock()
	t.cond.Broadcast()
}

func (t *tampon) terminer(err error) {
	t.mu.Lock()
	t.fini, t.err = true, err
	t.mu.Unlock()
	t.cond.Broadcast()
}

// abandonner réveille les lectures en attente : la réponse est remplacée ou supprimée.
func (t *tampon) abandonner() {
	t.mu.Lock()
	t.annule, t.fini = true, true
	t.mu.Unlock()
	t.cond.Broadcast()
}

// etat : durée d'audio disponible, génération finie ou non, erreur éventuelle.
func (t *tampon) etat() (time.Duration, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.taux == 0 {
		return 0, t.fini, t.err
	}
	return time.Duration(float64(len(t.data)) / float64(2*t.taux) * float64(time.Second)), t.fini, t.err
}

func (t *tampon) frequence() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.taux
}

// lireDepuis rend une copie des données après `debut`, en attendant qu'il y en ait.
// Rend (nil, true) quand tout est lu et que la génération est finie (ou abandonnée).
func (t *tampon) lireDepuis(debut int) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.data) <= debut && !t.fini {
		t.cond.Wait()
	}
	if t.annule || len(t.data) <= debut {
		return nil, true
	}
	return append([]byte(nil), t.data[debut:]...), false
}

// ---- génération ----------------------------------------------------------

type travail struct {
	id    int
	texte string
	tp    *tampon
	ctx   context.Context
}

type debutGeneration struct{ id int }

type genere struct {
	id     int
	moteur string
	err    error
}

// kyutaiDemarre : vrai pendant que le gestionnaire attend le serveur qu'il vient de lancer.
var kyutaiDemarre atomic.Bool

// generateur traite les travaux un par un, dans l'ordre d'arrivée.
func generateur(travaux <-chan travail, p *tea.Program) {
	for t := range travaux {
		if t.ctx.Err() != nil {
			t.tp.terminer(t.ctx.Err())
			continue
		}
		attendreKyutai(t.ctx)
		p.Send(debutGeneration{t.id})
		moteur, err := generer(t.ctx, lireConfig(), t.texte, t.tp)
		t.tp.terminer(err)
		p.Send(genere{id: t.id, moteur: moteur, err: err})
	}
}

// generer : Kyutai si le serveur répond, sinon Piper. Pas de repli si Kyutai a déjà
// produit de l'audio (on ne mélange pas deux voix dans une réponse).
func generer(ctx context.Context, cfg Config, texte string, tp *tampon) (string, error) {
	if kyutaiPret(cfg) {
		err := genererKyutai(ctx, cfg, texte, tp)
		if d, _, _ := tp.etat(); err == nil || ctx.Err() != nil || d > 0 {
			return "kyutai", err
		}
	}
	return "piper", genererPiper(ctx, cfg, texte, tp)
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

// clientCourt : vérifications d'état, sans connexion gardée ouverte côté serveur.
var clientCourt = &http.Client{
	Timeout:   2 * time.Second,
	Transport: &http.Transport{DisableKeepAlives: true},
}

func kyutaiPret(cfg Config) bool {
	r, err := clientCourt.Get(cfg.KyutaiURL + "/health")
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == http.StatusOK
}

// genererKyutai : flux continu (une seule génération, l'audio arrive trame par trame).
func genererKyutai(ctx context.Context, cfg Config, texte string, tp *tampon) error {
	corps, _ := json.Marshal(map[string]any{
		"input": texte, "voice": cfg.KyutaiVoix, "response_format": "pcm", "decoupage": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.KyutaiURL+"/v1/audio/speech", bytes.NewReader(corps))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Close = true
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("Kyutai : HTTP %d", r.StatusCode)
	}
	hz, _ := strconv.Atoi(r.Header.Get("X-Sample-Rate"))
	if hz <= 0 {
		hz = 24000
	}
	tp.definirTaux(hz)
	return copierDans(tp, r.Body)
}

func genererPiper(ctx context.Context, cfg Config, texte string, tp *tampon) error {
	modele, err := voixPiper(cfg.VoixPiper)
	if err != nil {
		return err
	}
	tp.definirTaux(tauxPiper(modele))
	cmd := exec.CommandContext(ctx, "piper", "--model", modele, "--output-raw")
	cmd.Stdin = strings.NewReader(texte)
	sortie, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var erreurs bytes.Buffer
	cmd.Stderr = &erreurs
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := copierDans(tp, sortie); err != nil {
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("Piper : %v %s", err, derniereLigne(erreurs.Bytes()))
	}
	return nil
}

func copierDans(tp *tampon, r io.Reader) error {
	buf := make([]byte, 16*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			tp.ecrire(buf[:n])
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
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

// tauxPiper lit la fréquence dans le .onnx.json de la voix (22050 Hz par défaut).
func tauxPiper(modele string) int {
	var conf struct {
		Audio struct {
			SampleRate int `json:"sample_rate"`
		} `json:"audio"`
	}
	if data, err := os.ReadFile(modele + ".json"); err == nil && json.Unmarshal(data, &conf) == nil && conf.Audio.SampleRate > 0 {
		return conf.Audio.SampleRate
	}
	return 22050
}

func derniereLigne(b []byte) string {
	lignes := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lignes[len(lignes)-1]
}

// ---- démarrage anticipé ----------------------------------------------------

// peutDemarrer : la lecture peut-elle commencer sans jamais rattraper la génération ?
// Avec ρ = secondes d'audio produites par seconde, A = durée totale estimée et R = ce qui
// reste à produire, il faut R/ρ ≤ A (la génération finit avant que la lecture n'arrive au
// bout). On garde 10 % de marge. `cps` = caractères dits par seconde (appris au fil des réponses).
func peutDemarrer(tp *tampon, texte string, debutGen time.Time, cps float64) bool {
	audio, fini, _ := tp.etat()
	if fini {
		return audio > 0
	}
	ecoule := time.Since(debutGen).Seconds()
	if audio < 1500*time.Millisecond || ecoule < 2 {
		return false
	}
	rho := audio.Seconds() / ecoule
	total := max(float64(len([]rune(texte)))/cps, audio.Seconds()*1.1)
	reste := total - audio.Seconds()
	return reste/rho <= 0.9*total
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

// demarrerLecture joue le tampon depuis le début avec paplay, en suivant la génération.
func demarrerLecture(id int, tp *tampon, volume string) (*lecture, tea.Cmd, error) {
	cmd := exec.Command("paplay", "--raw", "--format=s16le", "--channels=1",
		"--rate="+strconv.Itoa(tp.frequence()), "--volume="+volume)
	entree, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	go func() {
		defer entree.Close()
		for pos := 0; ; {
			b, fin := tp.lireDepuis(pos)
			if fin {
				return
			}
			if _, err := entree.Write(b); err != nil {
				return // lecture arrêtée
			}
			pos += len(b)
		}
	}()
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
