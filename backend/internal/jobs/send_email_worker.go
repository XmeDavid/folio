package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/xmedavid/folio/backend/internal/mailer"
)

type SendEmailWorker struct {
	river.WorkerDefaults[SendEmailArgs]
	mailer mailer.Mailer
}

func NewSendEmailWorker(m mailer.Mailer) *SendEmailWorker {
	return &SendEmailWorker{mailer: m}
}

func (w *SendEmailWorker) Work(ctx context.Context, job *river.Job[SendEmailArgs]) error {
	tmpl, err := mailer.LoadTemplate(job.Args.TemplateName)
	if err != nil {
		return fmt.Errorf("send_email: %w", err)
	}
	data, err := decodeData(job.Args.TemplateName, job.Args.Data)
	if err != nil {
		return err
	}
	msg, err := tmpl.Render(data)
	if err != nil {
		return err
	}
	msg.To = job.Args.ToAddress
	msg.Template = job.Args.TemplateName
	msg.Data = job.Args.Data
	return w.mailer.Send(ctx, msg)
}

func decodeData(name string, raw map[string]any) (any, error) {
	bs, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	switch name {
	case "verify_email":
		var d mailer.VerifyEmailData
		return d, json.Unmarshal(bs, &d)
	case "password_reset":
		var d mailer.PasswordResetData
		return d, json.Unmarshal(bs, &d)
	case "email_change_new":
		var d mailer.EmailChangeNewData
		return d, json.Unmarshal(bs, &d)
	case "email_change_old_notice":
		var d mailer.EmailChangeOldNoticeData
		return d, json.Unmarshal(bs, &d)
	case "invite":
		var d mailer.InviteData
		return d, json.Unmarshal(bs, &d)
	default:
		return nil, fmt.Errorf("send_email: unknown template %q", name)
	}
}
