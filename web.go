package main

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/lsongdev/chat-server/templates"
)

type Web struct {
	app    *template.Template
	invite *template.Template
}

func NewWeb() (*Web, error) {
	app, err := template.ParseFS(templates.Files, "app.html")
	if err != nil {
		return nil, err
	}
	invite, err := template.ParseFS(templates.Files, "invite.html")
	if err != nil {
		return nil, err
	}
	return &Web{app: app, invite: invite}, nil
}

func (web *Web) Home(w http.ResponseWriter, r *http.Request) {
	if _, ok := currentUser(r.Context()); !ok {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.app.Execute(w, nil)
}

func (web *Web) Invite(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/invite/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	if _, ok := currentUser(r.Context()); !ok {
		http.Redirect(w, r, "/auth/login?return_to="+url.QueryEscape(r.URL.Path), http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = web.invite.Execute(w, map[string]string{"Token": token})
}
