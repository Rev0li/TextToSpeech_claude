package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config : réglages lus dans ~/.claude/.tts-config (le même fichier que le plugin tts).
// Relu avant chaque génération : un changement de voix compte tout de suite.
type Config struct {
	KyutaiURL  string
	KyutaiVoix string
	VoixPiper  string
	Volume     string
	// KyutaiScript : chemin de kyutai/kyutai.sh (écrit par install.sh). Si présent, le
	// gestionnaire démarre le serveur à l'ouverture et l'arrête en quittant.
	KyutaiScript string
}

func lireConfig() Config {
	c := Config{
		KyutaiURL:  "http://127.0.0.1:8002",
		KyutaiVoix: "livre",
		VoixPiper:  "fr_FR-siwis",
		Volume:     "40000",
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return c
	}
	f, err := os.Open(filepath.Join(home, ".claude", ".tts-config"))
	if err != nil {
		return c
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ligne := strings.TrimSpace(sc.Text())
		if ligne == "" || strings.HasPrefix(ligne, "#") {
			continue
		}
		cle, valeur, ok := strings.Cut(ligne, "=")
		if !ok || valeur == "" {
			continue
		}
		switch cle {
		case "KYUTAI_URL":
			c.KyutaiURL = strings.TrimRight(valeur, "/")
		case "KYUTAI_VOICE":
			c.KyutaiVoix = valeur
		case "VOICE":
			c.VoixPiper = valeur
		case "VOLUME":
			c.Volume = valeur
		case "KYUTAI_SCRIPT":
			c.KyutaiScript = valeur
		}
	}
	return c
}

// dossierExecution : $XDG_RUNTIME_DIR (privé, vidé à la déconnexion), sinon le dossier temporaire.
func dossierExecution() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

func cheminSocket() string {
	return filepath.Join(dossierExecution(), "claude-voix.sock")
}

// ecrireConfig remplace (ou ajoute) CLÉ=valeur dans ~/.claude/.tts-config, sans toucher au reste.
func ecrireConfig(cle, valeur string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	chemin := filepath.Join(home, ".claude", ".tts-config")
	data, err := os.ReadFile(chemin)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lignes := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(data) == 0 {
		lignes = nil
	}
	trouve := false
	for i, l := range lignes {
		if strings.HasPrefix(l, cle+"=") {
			lignes[i], trouve = cle+"="+valeur, true
		}
	}
	if !trouve {
		lignes = append(lignes, cle+"="+valeur)
	}
	if err := os.MkdirAll(filepath.Dir(chemin), 0o700); err != nil {
		return err
	}
	return os.WriteFile(chemin, []byte(strings.Join(lignes, "\n")+"\n"), 0o600)
}
