package jobs

type SendEmailArgs struct {
	TemplateName   string         `json:"template"`
	ToAddress      string         `json:"to"`
	Data           map[string]any `json:"data"`
	IdempotencyKey string         `json:"idempotency_key"`
}

func (SendEmailArgs) Kind() string { return "send_email" }

type SweepSoftDeletedTenantsArgs struct{}

func (SweepSoftDeletedTenantsArgs) Kind() string { return "sweep_soft_deleted_tenants" }
