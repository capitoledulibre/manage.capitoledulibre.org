package server

import (
	"time"

	"manage/internal/store"
)

// Step is one milestone of the partnership progress bar.
type Step struct {
	Key  string
	Done bool
	// Staff is true when the step is completed by the organizers rather
	// than by the partner.
	Staff bool
}

// Todo is an action the partner still has to take.
type Todo struct {
	Key      string
	Link     string
	Deadline *time.Time
	// Missing names what is left to fill in, as i18n keys.
	Missing []string
}

type Progress struct {
	Steps   []Step
	Todos   []Todo
	Done    int
	Percent int
	Ready   bool
}

// computeProgress builds the progress bar and to-do list. query is appended
// to edition-specific links to keep the selected edition.
func computeProgress(p *store.Partnership, org *store.Organization, e *store.Event, attendees int, query string) Progress {
	quoteSent := p.QuoteSentAt != nil || p.QuoteFile != ""
	invoiceSent := p.InvoiceSentAt != nil || p.InvoiceFile != ""
	info := org.BillingComplete() && org.CommunicationComplete()

	pr := Progress{Steps: []Step{
		{Key: "step.quote_sent", Done: quoteSent, Staff: true},
		{Key: "step.quote_signed", Done: p.QuoteSignedAt != nil, Staff: true},
		{Key: "step.invoice_sent", Done: invoiceSent, Staff: true},
		{Key: "step.invoice_paid", Done: p.InvoicePaidAt != nil, Staff: true},
		{Key: "step.info", Done: info},
		{Key: "step.logo", Done: org.LogoFile != ""},
		{Key: "step.attendees", Done: attendees > 0},
	}}

	for _, s := range pr.Steps {
		if s.Done {
			pr.Done++
		}
	}

	pr.Percent = pr.Done * 100 / len(pr.Steps)
	pr.Ready = pr.Done == len(pr.Steps)

	if !org.BillingComplete() {
		pr.Todos = append(pr.Todos, Todo{Key: "todo.billing", Link: "/partners/organization", Missing: org.MissingBilling()})
	}

	if !org.CommunicationComplete() {
		pr.Todos = append(pr.Todos, Todo{Key: "todo.communication", Link: "/partners/organization", Missing: org.MissingCommunication()})
	}

	if org.LogoFile == "" {
		pr.Todos = append(pr.Todos, Todo{Key: "todo.logo", Link: "/partners/logo", Deadline: e.LogoDeadline})
	}

	if quoteSent && p.QuoteSignedAt == nil {
		pr.Todos = append(pr.Todos, Todo{Key: "todo.sign_quote", Link: documentLink(e, p, "quote")})
	}

	if invoiceSent && p.InvoicePaidAt == nil {
		pr.Todos = append(pr.Todos, Todo{Key: "todo.pay_invoice", Link: documentLink(e, p, "invoice")})
	}

	// Past the lock, the list no longer opens: the to-do says whom to ask.
	switch {
	case attendees == 0 && e.AttendeesLocked():
		pr.Todos = append(pr.Todos, Todo{Key: "todo.attendees_locked"})
	case attendees == 0:
		pr.Todos = append(pr.Todos, Todo{Key: "todo.attendees", Link: "/partners/attendees" + query, Deadline: e.AttendeesDeadline})
	}

	return pr
}

func documentLink(e *store.Event, p *store.Partnership, kind string) string {
	switch {
	case kind == "quote" && p.QuoteFile == "", kind == "invoice" && p.InvoiceFile == "":
		return ""
	}

	return "/partners/documents/" + e.Slug + "/" + kind
}
