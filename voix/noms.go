package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Noms : nom affiché de chaque fenêtre Claude Code. Par défaut le nom du dossier ;
// un renommage vaut pour la session et pour les prochaines sessions du même dossier.
type Noms struct {
	Sessions map[string]string `json:"sessions"`
	Dossiers map[string]string `json:"dossiers"`
	chemin   string
}

func chargerNoms() *Noms {
	n := &Noms{Sessions: map[string]string{}, Dossiers: map[string]string{}}
	if d, err := os.UserConfigDir(); err == nil {
		n.chemin = filepath.Join(d, "claude-voix", "noms.json")
		if data, err := os.ReadFile(n.chemin); err == nil {
			_ = json.Unmarshal(data, n)
		}
	}
	if n.Sessions == nil {
		n.Sessions = map[string]string{}
	}
	if n.Dossiers == nil {
		n.Dossiers = map[string]string{}
	}
	return n
}

func (n *Noms) Nom(session, cwd string) string {
	if v, ok := n.Sessions[session]; ok {
		return v
	}
	if v, ok := n.Dossiers[cwd]; ok {
		return v
	}
	if cwd == "" || cwd == "/" {
		return "autre"
	}
	return filepath.Base(cwd)
}

func (n *Noms) Renommer(session, cwd, nom string) error {
	if session != "" {
		n.Sessions[session] = nom
	}
	if cwd != "" {
		n.Dossiers[cwd] = nom
	}
	if n.chemin == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(n.chemin), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(n.chemin, data, 0o600)
}
