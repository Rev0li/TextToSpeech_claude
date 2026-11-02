package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Message : ce que le plugin tts envoie au gestionnaire, une ligne JSON par connexion.
type Message struct {
	Type    string `json:"type"` // "reponse" ou "couper"
	Session string `json:"session,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Texte   string `json:"texte,omitempty"`
}

type messageRecu Message

const tailleMaxMessage = 1 << 20

// ecouter ouvre le socket du gestionnaire (droits 600) et transmet chaque message au programme.
func ecouter(chemin string, p *tea.Program) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", chemin, 500*time.Millisecond); err == nil {
		c.Close()
		return nil, errors.New("claude-voix est déjà ouvert dans un autre terminal")
	}
	_ = os.Remove(chemin) // socket orphelin d'une session précédente
	l, err := net.Listen("unix", chemin)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(chemin, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				var m Message
				if err := json.NewDecoder(io.LimitReader(c, tailleMaxMessage)).Decode(&m); err != nil {
					return
				}
				p.Send(messageRecu(m))
				_, _ = io.WriteString(c, "ok\n")
			}(conn)
		}
	}()
	return l, nil
}

// envoyer : côté plugin. Échoue vite si le gestionnaire n'est pas ouvert.
func envoyer(chemin string, m Message) error {
	c, err := net.DialTimeout("unix", chemin, time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(c).Encode(m); err != nil {
		return err
	}
	_, err = bufio.NewReader(c).ReadString('\n')
	return err
}
