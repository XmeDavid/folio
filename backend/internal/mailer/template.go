package mailer

import (
	"bytes"
	"fmt"
	htmltmpl "html/template"
	texttmpl "text/template"
)

type Template struct {
	name    string
	subject func(data any) string
	html    *htmltmpl.Template
	text    *texttmpl.Template
}

func (t *Template) Render(data any) (Message, error) {
	var hbuf, tbuf bytes.Buffer
	view := struct {
		Subject string
		Data    any
	}{Subject: t.subject(data), Data: data}
	if err := t.html.ExecuteTemplate(&hbuf, "layout", view); err != nil {
		return Message{}, fmt.Errorf("template %s html: %w", t.name, err)
	}
	if err := t.text.Execute(&tbuf, data); err != nil {
		return Message{}, fmt.Errorf("template %s text: %w", t.name, err)
	}
	return Message{Subject: view.Subject, HTML: hbuf.String(), Text: tbuf.String()}, nil
}

type VerifyEmailData struct {
	DisplayName string
	VerifyURL   string
}

type PasswordResetData struct {
	DisplayName string
	ResetURL    string
	ExpiresIn   string
}

type EmailChangeNewData struct {
	DisplayName string
	ConfirmURL  string
	OldEmail    string
	NewEmail    string
}

type EmailChangeOldNoticeData struct {
	DisplayName string
	OldEmail    string
	NewEmail    string
}

type InviteData struct {
	InviterName string
	TenantName  string
	Role        string
	AcceptURL   string
}

func LoadTemplate(name string) (*Template, error) {
	subjects := map[string]func(any) string{
		"verify_email":            func(any) string { return "Verify your Folio email" },
		"password_reset":          func(any) string { return "Reset your Folio password" },
		"email_change_new":        func(any) string { return "Confirm your new Folio email" },
		"email_change_old_notice": func(any) string { return "Your Folio email address was changed" },
		"invite": func(d any) string {
			if i, ok := d.(InviteData); ok && i.TenantName != "" {
				return fmt.Sprintf("You're invited to join %s on Folio", i.TenantName)
			}
			return "You're invited on Folio"
		},
	}
	subj, ok := subjects[name]
	if !ok {
		return nil, fmt.Errorf("mailer: unknown template %q", name)
	}
	htmlT, err := htmltmpl.ParseFS(templateFS, "templates/_layout.html.tmpl", "templates/"+name+".html.tmpl")
	if err != nil {
		return nil, err
	}
	textT, err := texttmpl.ParseFS(templateFS, "templates/"+name+".txt.tmpl")
	if err != nil {
		return nil, err
	}
	return &Template{name: name, subject: subj, html: htmlT, text: textT}, nil
}
