// Package i18n holds the French and English strings of the partner-facing
// interface and emails.
package i18n

import (
	"fmt"
	"log/slog"
	"strings"
)

// site is the event name substituted for "{site}" in messages, set with
// SetSite.
var site = "Example Conf"

// SetSite sets the event name used in messages. It is called once at
// startup.
func SetSite(name string) { site = name }

// Site returns the event name.
func Site() string { return site }

// T returns the message for key in lang (falling back to French), formatted
// with args when given. "{site}" is replaced by the event name.
func T(lang, key string, args ...any) string {
	m, ok := messages[key]
	if !ok {
		slog.Warn("missing translation", "key", key)

		return key
	}

	s := m[0]
	if lang == "en" && m[1] != "" {
		s = m[1]
	}

	if len(args) > 0 {
		// Escape % so the event name cannot be read as a format verb.
		s = strings.ReplaceAll(s, "{site}", strings.ReplaceAll(site, "%", "%%"))

		return fmt.Sprintf(s, args...)
	}

	return strings.ReplaceAll(s, "{site}", site)
}

// Has reports whether a key exists in the catalog.
func Has(key string) bool {
	_, ok := messages[key]

	return ok
}

// messages maps a key to its {French, English} text.
var messages = map[string][2]string{
	// Navigation and generic actions.
	"nav.dashboard":         {"Tableau de bord", "Dashboard"},
	"nav.organization":      {"Organisation", "Organization"},
	"nav.logo":              {"Logo", "Logo"},
	"nav.attendees":         {"Participants", "Attendees"},
	"nav.jobs":              {"Offres d'emploi", "Job offers"},
	"nav.managers":          {"Managers", "Managers"},
	"nav.logout":            {"Déconnexion", "Log out"},
	"nav.jobs_public":       {"Offres d'emploi des partenaires", "Partner job offers"},
	"action.save":           {"Enregistrer", "Save"},
	"action.add":            {"Ajouter", "Add"},
	"action.edit":           {"Modifier", "Edit"},
	"action.delete":         {"Supprimer", "Delete"},
	"action.confirm_delete": {"Confirmer la suppression ?", "Really delete?"},
	"upload.add":            {"Ajouter", "Add"},
	"upload.replace":        {"Remplacer", "Replace"},
	"upload.drop":           {"Glissez-déposez votre fichier ici", "Drag and drop your file here"},
	"upload.pages":          {"Page 1 sur %d", "Page 1 of %d"},
	"upload.preview":        {"Aperçu du fichier", "File preview"},
	"upload.browse":         {"Choisir un fichier", "Choose a file"},
	"upload.max":            {"max.", "max."},
	"upload.save":           {"Enregistrer", "Save"},
	"upload.notify":         {"Prévenir les managers par email", "Notify the managers by email"},
	"upload.too_large":      {"Ce fichier est trop volumineux.", "This file is too large."},
	"upload.bad_type":       {"Ce format n'est pas accepté.", "This format is not accepted."},
	"upload.kb":             {"Ko", "KB"},
	"upload.mb":             {"Mo", "MB"},
	"confirm.title":         {"Confirmation", "Confirmation"},
	"confirm.ok":            {"Confirmer", "Confirm"},
	"confirm.cancel":        {"Annuler", "Cancel"},
	"menu.label":            {"Menu du compte", "Account menu"},
	"menu.profile":          {"Mon profil", "My profile"},
	"theme.label":           {"Thème", "Theme"},
	"theme.system":          {"Thème du système", "System theme"},
	"theme.light":           {"Thème clair", "Light theme"},
	"theme.dark":            {"Thème sombre", "Dark theme"},

	// Flash messages.
	"flash.saved":            {"Modifications enregistrées.", "Changes saved."},
	"flash.deleted":          {"Élément supprimé.", "Item deleted."},
	"flash.logo_saved":       {"Logo enregistré, merci !", "Logo saved, thank you!"},
	"flash.invited":          {"Invitation envoyée.", "Invitation sent."},
	"flash.last_manager":     {"Impossible de retirer le dernier manager.", "The last manager cannot be removed."},
	"flash.invalid_email":    {"Adresse email invalide.", "Invalid email address."},
	"flash.cloned":           {"Niveaux et contreparties copiés. Vérifiez les prix avant d'ouvrir l'édition.", "Levels and perks copied. Check the prices before opening the edition."},
	"flash.clone_nothing":    {"Rien à copier : cette édition a déjà tous ces niveaux et contreparties.", "Nothing to copy: this edition already has all these levels and perks."},
	"flash.renewed":          {"Partenariat reconduit.", "Partnership renewed."},
	"flash.already_renewed":  {"Cette organisation a déjà un partenariat pour cette édition.", "This organization already has a partnership for this edition."},
	"flash.confirm_mismatch": {"La confirmation ne correspond pas.", "Confirmation does not match."},
	"flash.upload_missing":   {"Aucun fichier sélectionné.", "No file selected."},
	"flash.upload_too_large": {"Fichier trop volumineux.", "File too large."},
	"flash.upload_bad_logo":  {"Format non accepté : envoyez un SVG, PNG, JPEG ou WebP.", "Unsupported format: please upload an SVG, PNG, JPEG or WebP file."},
	"flash.upload_bad_pdf":   {"Seuls les fichiers PDF sont acceptés.", "Only PDF files are accepted."},

	// Errors.
	"error.title":       {"Erreur", "Error"},
	"error.not_found":   {"Page introuvable", "Page not found"},
	"error.forbidden":   {"Accès refusé", "Access denied"},
	"error.bad_request": {"Requête invalide", "Invalid request"},
	"error.internal":    {"Une erreur est survenue", "Something went wrong"},
	"error.home":        {"Retour à l'accueil", "Back to home"},

	// Login.
	"login.title":             {"Connexion", "Log in"},
	"login.heading":           {"Espace partenaires · {site}", "{site} partner area"},
	"login.intro":             {"Merci de soutenir {site} ! Cet espace vous permet de suivre votre partenariat de bout en bout.", "Thank you for supporting {site}! This area lets you follow your partnership from start to finish."},
	"login.feature_progress":  {"Suivre l'avancement de votre partenariat", "Track the progress of your partnership"},
	"login.feature_documents": {"Consulter devis, facture, plaquette et guide du sponsor", "Access your quote, invoice, brochure and sponsor guide"},
	"login.feature_logo":      {"Envoyer votre logo et vos informations", "Upload your logo and company details"},
	"login.feature_attendees": {"Inscrire les personnes présentes sur l'événement", "Register the people attending the event"},
	"login.feature_jobs":      {"Publier vos offres d'emploi", "Publish your job offers"},
	"login.email":             {"Adresse email", "Email address"},
	"login.submit":            {"Recevoir un lien de connexion", "Send me a login link"},
	"login.help":              {"Pas de mot de passe : nous vous envoyons un lien de connexion à usage unique.", "No password: we email you a single-use login link."},
	"login.invalid_email":     {"Adresse email invalide.", "Invalid email address."},
	"login.link_invalid":      {"Ce lien de connexion est invalide ou a expiré. Demandez-en un nouveau.", "This login link is invalid or has expired. Please request a new one."},
	"login.sent_title":        {"Vérifiez votre boîte mail", "Check your inbox"},
	"login.sent_body":         {"Si %s est une adresse connue de {site}, un lien de connexion vient d'y être envoyé. Pensez à vérifier vos spams.", "If %s is an address {site} knows, a login link has just been sent to it. Please check your spam folder too."},
	"home.title":              {"Mes espaces", "My spaces"},
	"home.heading":            {"Bonjour %s", "Hello %s"},
	"home.empty":              {"Vous êtes connecté·e, mais aucun espace ne vous est ouvert sur {site} pour l'instant.", "You are logged in, but no space on {site} is open to you yet."},
	"login.back":              {"Retour", "Back"},
	"login.confirm_title":     {"Connexion", "Log in"},
	"login.confirm_body":      {"Vous allez vous connecter en tant que %s.", "You are about to log in as %s."},
	"login.confirm_submit":    {"Me connecter", "Log me in"},

	"choose.title": {"Choisissez une organisation", "Choose an organization"},
	"choose.help":  {"Votre adresse email est manager de plusieurs organisations.", "Your email address manages several organizations."},

	// Dashboard.
	"dashboard.title":               {"Tableau de bord", "Dashboard"},
	"dashboard.welcome":             {"Bienvenue dans votre espace partenaire", "Welcome to your partner area"},
	"dashboard.switch_org":          {"Changer d'organisation", "Switch organization"},
	"dashboard.edition":             {"Édition", "Edition"},
	"dashboard.no_partnership":      {"Aucun partenariat en cours", "No active partnership"},
	"dashboard.no_partnership_help": {"Votre partenariat apparaîtra ici dès que notre équipe vous aura transmis une proposition. Vous pouvez déjà compléter vos informations et votre logo.", "Your partnership will appear here as soon as our team sends you a proposal. You can already fill in your details and logo."},
	"dashboard.progress":            {"Avancement", "Progress"},
	"dashboard.ready":               {"Tout est prêt, merci !", "All set, thank you!"},
	"dashboard.todo":                {"Ce qu'il vous reste à faire", "What's left to do"},
	"dashboard.todo_none":           {"Rien pour le moment, tout est en ordre.", "Nothing for now, everything is in order."},
	"dashboard.before":              {"avant le", "before"},
	"dashboard.do_it":               {"Y aller", "Go"},
	"dashboard.documents":           {"Documents", "Documents"},
	"dashboard.notes":               {"Informations complémentaires", "Additional information"},
	"dashboard.po_number":           {"Numéro de bon de commande", "Purchase order number"},
	"dashboard.po_help":             {"Si votre service comptable exige un bon de commande, indiquez son numéro : il figurera sur la facture.", "If your accounting department requires a purchase order, enter its number: it will appear on the invoice."},
	"dashboard.notes_label":         {"Message à l'équipe (besoins particuliers, questions…)", "Message to the team (special needs, questions…)"},
	"dashboard.level":               {"Niveau de partenariat", "Sponsorship level"},
	"dashboard.level_pending":       {"Niveau en cours de définition.", "Level to be confirmed."},
	"dashboard.perks":               {"Contreparties", "Benefits"},
	"dashboard.perks_help":          {"✓ = réalisé par notre équipe.", "✓ = delivered by our team."},
	"dashboard.summary":             {"En bref", "At a glance"},
	"dashboard.add":                 {"Ajouter", "Add"},
	"dashboard.event_date":          {"Événement", "Event"},
	"dashboard.contact":             {"Votre contact", "Your contact"},
	"dashboard.contact_help":        {"Une question sur votre partenariat ? Écrivez-lui directement.", "A question about your partnership? Write to them directly."},
	"dashboard.history":             {"Merci pour votre fidélité !", "Thank you for your loyalty!"},
	"badge.partner":                 {"Partenaire %s %d", "%s sponsor %d"},
	"badge.partner_nolevel":         {"Partenaire %d", "Sponsor %d"},

	"step.quote_sent":   {"Devis reçu", "Quote received"},
	"step.quote_signed": {"Devis signé", "Quote signed"},
	"step.invoice_sent": {"Facture reçue", "Invoice received"},
	"step.invoice_paid": {"Facture réglée", "Invoice paid"},
	"step.info":         {"Informations complètes", "Details complete"},
	"step.logo":         {"Logo fourni", "Logo provided"},
	"step.attendees":    {"Participants inscrits", "Attendees registered"},
	"step.by_staff":     {"par l'équipe", "by our team"},
	"step.by_you":       {"par vous", "by you"},

	"todo.billing":       {"Compléter vos informations de facturation", "Fill in your billing details"},
	"todo.communication": {"Renseigner votre site web et une description", "Add your website and a description"},
	"todo.logo":          {"Envoyer votre logo", "Upload your logo"},
	"todo.sign_quote":    {"Nous retourner le devis signé", "Send us the signed quote"},
	"todo.pay_invoice":   {"Régler la facture", "Pay the invoice"},
	"todo.attendees":     {"Inscrire les personnes présentes", "Register your attendees"},

	"doc.quote":         {"Devis", "Quote"},
	"doc.invoice":       {"Facture", "Invoice"},
	"doc.brochure":      {"Plaquette partenaires", "Sponsorship brochure"},
	"doc.guide":         {"Guide du sponsor", "Sponsor guide"},
	"doc.view":          {"Consulter (PDF)", "View (PDF)"},
	"doc.not_available": {"Pas encore disponible", "Not available yet"},
	"doc.sent_on":       {"envoyé le", "sent on"},
	"doc.signed_on":     {"signé le", "signed on"},
	"doc.paid_on":       {"réglée le", "paid on"},
	"doc.quote_sent":    {"Envoyé", "Sent"},
	"doc.quote_signed":  {"Signé", "Signed"},
	"doc.invoice_sent":  {"Envoyée", "Sent"},
	"doc.invoice_paid":  {"Réglée", "Paid"},
	"doc.awaiting":      {"en attente", "pending"},
	"doc.payment":       {"Règlement", "Payment"},
	"doc.po_reference":  {"Bon de commande : %s", "Purchase order: %s"},

	// Taxes, for level prices.
	"tax.ht":         {"HT", "excl. VAT"},
	"tax.ttc":        {"TTC", "incl. VAT"},
	"tax.none":       {"TVA non applicable", "VAT not applicable"},
	"tax_short.ht":   {"HT", "excl. VAT"},
	"tax_short.ttc":  {"TTC", "incl. VAT"},
	"tax_short.none": {"sans TVA", "no VAT"},

	// Social media posts.
	"social.title":         {"Annonces sur les réseaux sociaux", "Social media announcements"},
	"social.network_other": {"Autre", "Other"},
	"social.published":     {"Publié", "Published"},
	"social.planned_on":    {"Prévu le", "Planned on"},
	"social.to_schedule":   {"Date à définir", "Date to be defined"},
	"social.upcoming":      {"À venir", "Upcoming"},
	"social.view":          {"Voir le post", "View post"},
	"social.help":          {"N'hésitez pas à partager et commenter nos posts une fois publiés !", "Feel free to share and comment on our posts once published!"},

	// Organization.
	"organization.title":            {"Votre organisation", "Your organization"},
	"organization.intro":            {"Ces informations servent à la facturation et à votre présentation sur le site de l'événement.", "This information is used for invoicing and to present you on the event website."},
	"organization.general":          {"Présentation", "Presentation"},
	"organization.billing":          {"Facturation", "Billing"},
	"organization.name":             {"Nom affiché", "Display name"},
	"organization.website":          {"Site web", "Website"},
	"organization.description_fr":   {"Description (français)", "Description (French)"},
	"organization.description_en":   {"Description (anglais)", "Description (English)"},
	"organization.description_help": {"Quelques lignes utilisées sur le site et les réseaux sociaux. Une seule langue suffit.", "A few lines used on the website and social networks. One language is enough."},
	"organization.legal_name":       {"Raison sociale", "Legal name"},
	"organization.address":          {"Adresse", "Address"},
	"organization.zip_code":         {"Code postal", "Postal code"},
	"organization.city":             {"Ville", "City"},
	"organization.country":          {"Pays", "Country"},
	"organization.vat":              {"N° de TVA intracommunautaire", "VAT number"},
	"organization.name_required":    {"Le nom est obligatoire.", "The name is required."},

	// Logo.
	"logo.title":           {"Logo", "Logo"},
	"logo.intro":           {"Votre logo sera affiché sur le site, les supports imprimés et les vidéos de l'événement.", "Your logo will be displayed on the website, printed material and event videos."},
	"logo.variant_default": {"Logo principal", "Main logo"},
	"logo.variant_dark":    {"Version pour fond sombre (optionnel)", "Dark background version (optional)"},
	"logo.none":            {"Aucun logo", "No logo yet"},
	"logo.file":            {"Fichier du logo", "Logo file"},
	"logo.upload":          {"Envoyer", "Upload"},
	"logo.help":            {"Format vectoriel (SVG) de préférence, sinon PNG avec fond transparent d'au moins 1000 px de large. 10 Mo maximum.", "Vector format (SVG) preferred, otherwise a PNG with a transparent background at least 1000 px wide. 10 MB max."},
	"logo.history":         {"Historique", "History"},

	// Attendees.
	"attendees.title":               {"Participants", "Attendees"},
	"attendees.intro":               {"Indiquez les personnes de votre organisation présentes à %s (stand, visite). Elles recevront un badge partenaire.", "List the people from your organization attending %s (booth staff, visitors). They will receive a partner badge."},
	"attendees.deadline":            {"À renseigner avant le %s.", "Please fill in before %s."},
	"attendees.list":                {"Personnes inscrites", "Registered people"},
	"attendees.none":                {"Personne pour l'instant.", "Nobody yet."},
	"attendees.add":                 {"Ajouter une personne", "Add a person"},
	"attendees.name":                {"Nom", "Name"},
	"attendees.first_name":          {"Prénom", "First name"},
	"attendees.last_name":           {"Nom", "Last name"},
	"attendees.email":               {"Email", "Email"},
	"attendees.role":                {"Rôle", "Role"},
	"attendees.role_booth":          {"Stand", "Booth staff"},
	"attendees.role_visitor":        {"Visiteur", "Visitor"},
	"attendees.dietary":             {"Régime alimentaire", "Dietary requirements"},
	"attendees.dietary_placeholder": {"végétarien, allergie…", "vegetarian, allergy…"},
	"attendees.name_required":       {"Le prénom et le nom sont obligatoires.", "First and last names are required."},
	"attendees.full":                {"Vous avez atteint le nombre de participants inclus dans votre niveau. Contactez-nous si besoin.", "You have reached the number of attendees included in your level. Contact us if needed."},
	"attendees.locked":              {"La date limite est passée : contactez-nous pour toute modification.", "The deadline has passed: please contact us for any change."},

	// Jobs.
	"jobs.title":              {"Nos partenaires recrutent", "Our partners are hiring"},
	"jobs.intro":              {"Les entreprises qui soutiennent {site} recrutent. Venez les rencontrer sur leur stand !", "The companies supporting {site} are hiring. Come and meet them at their booth!"},
	"jobs.none":               {"Aucune offre pour le moment. Revenez bientôt !", "No offers yet. Come back soon!"},
	"jobs.sponsor":            {"Partenaire", "Sponsor"},
	"jobs.website":            {"Site web", "Website"},
	"jobs.manage_title":       {"Offres d'emploi", "Job offers"},
	"jobs.manage_intro":       {"Vos offres sont publiées sur la page publique", "Your offers are published on the public page"},
	"jobs.manage_none":        {"Aucune offre. Ajoutez-en une ou un lien vers votre page carrières.", "No offers yet. Add one, or a link to your careers page."},
	"jobs.new":                {"Nouvelle offre", "New offer"},
	"jobs.edit":               {"Modifier l'offre", "Edit offer"},
	"jobs.job_title":          {"Intitulé", "Title"},
	"jobs.contract":           {"Contrat", "Contract"},
	"jobs.location":           {"Lieu", "Location"},
	"jobs.url":                {"Lien vers l'offre ou votre page carrières", "Link to the offer or your careers page"},
	"jobs.description":        {"Description", "Description"},
	"jobs.expires_at":         {"Date d'expiration (optionnel)", "Expiry date (optional)"},
	"jobs.status":             {"Statut", "Status"},
	"jobs.published":          {"Publiée", "Published"},
	"jobs.hidden":             {"Masquée par l'équipe", "Hidden by the team"},
	"jobs.expired":            {"Expirée", "Expired"},
	"jobs.title_required":     {"L'intitulé est obligatoire.", "The title is required."},
	"jobs.url_or_description": {"Indiquez un lien ou une description.", "Provide a link or a description."},

	// Managers.
	"managers.title":       {"Managers", "Managers"},
	"managers.intro":       {"Les managers peuvent se connecter à cet espace et gérer le partenariat. Invitez vos collègues (communication, comptabilité…).", "Managers can log in to this area and manage the partnership. Invite your colleagues (marketing, accounting…)."},
	"managers.person":      {"Personne", "Person"},
	"managers.last_login":  {"Dernière connexion", "Last login"},
	"managers.never":       {"jamais", "never"},
	"managers.remove":      {"Retirer", "Remove"},
	"managers.invite":      {"Inviter un manager", "Invite a manager"},
	"managers.name":        {"Nom", "Name"},
	"managers.lang":        {"Langue des emails", "Email language"},
	"managers.send_invite": {"Envoyer l'invitation", "Send invitation"},
	"managers.invite_help": {"La personne reçoit un lien de connexion valable 7 jours.", "The person receives a login link valid for 7 days."},
	"managers.already":     {"Cette personne est déjà manager.", "This person is already a manager."},

	"profile.title": {"Mon profil", "My profile"},

	// Emails. Bodies are plain text.
	"mail.login.subject": {"Votre lien de connexion — {site}", "Your login link — {site}"},
	"mail.login.body": {
		"Bonjour,\n\nVoici votre lien de connexion à votre espace partenaire ({site}) :\n\n%s\n\nCe lien est valable %d minutes et ne peut être utilisé qu'une fois.\nSi vous n'êtes pas à l'origine de cette demande, ignorez ce message.\n\nL'équipe {site}\n",
		"Hello,\n\nHere is your login link to the {site} partner area:\n\n%s\n\nThis link is valid for %d minutes and can only be used once.\nIf you did not request it, just ignore this email.\n\nThe {site} team\n",
	},
	"mail.invite.subject": {"Invitation à l'espace partenaire %s — {site}", "Invitation to the %s partner area — {site}"},
	"mail.invite.body": {
		"Bonjour,\n\n%s vous invite à gérer le partenariat de %s avec {site}.\n\nConnectez-vous avec ce lien (valable 7 jours) :\n\n%s\n\nPar la suite, vous pourrez demander un nouveau lien à tout moment sur %s\n\nL'équipe {site}\n",
		"Hello,\n\n%s invites you to manage the %s partnership with {site}.\n\nLog in with this link (valid for 7 days):\n\n%s\n\nAfterwards you can request a new link at any time on %s\n\nThe {site} team\n",
	},
	"mail.quote.subject": {"Votre devis est disponible — %s", "Your quote is available — %s"},
	"mail.quote.body": {
		"Bonjour,\n\nVotre devis pour {site} est disponible dans votre espace partenaire :\n\n%s\n\nMerci de nous le retourner signé.\n\nL'équipe {site}\n",
		"Hello,\n\nYour quote for {site} is available in your partner area:\n\n%s\n\nPlease send it back to us signed.\n\nThe {site} team\n",
	},
	"mail.invoice.subject": {"Votre facture est disponible — %s", "Your invoice is available — %s"},
	"mail.invoice.body": {
		"Bonjour,\n\nVotre facture pour {site} est disponible dans votre espace partenaire :\n\n%s\n\nMerci pour votre soutien !\n\nL'équipe {site}\n",
		"Hello,\n\nYour invoice for {site} is available in your partner area:\n\n%s\n\nThank you for your support!\n\nThe {site} team\n",
	},
	"mail.post.subject": {"Votre partenariat est annoncé — %s", "Your partnership has been announced — %s"},
	"mail.post.body": {
		"Bonjour,\n\nNous venons d'annoncer votre partenariat sur %[2]s :\n\n%[3]s\n\nN'hésitez pas à le partager et le commenter !\n\nVotre espace partenaire : %[1]s\n\nL'équipe {site}\n",
		"Hello,\n\nWe have just announced your partnership on %[2]s:\n\n%[3]s\n\nFeel free to share and comment on it!\n\nYour partner area: %[1]s\n\nThe {site} team\n",
	},
	"mail.reminder.subject":   {"Rappel : éléments attendus pour %s", "Reminder: items needed for %s"},
	"mail.reminder.logo":      {"votre logo, avant le %s", "your logo, before %s"},
	"mail.reminder.attendees": {"la liste des personnes présentes, avant le %s", "the list of attendees, before %s"},
	"mail.reminder.body": {
		"Bonjour,\n\nPour préparer au mieux votre venue à l'événement ({site}), il nous manque encore :\n\n%s\n\nVous pouvez les renseigner dans votre espace partenaire :\n\n%s\n\nMerci !\nL'équipe {site}\n",
		"Hello,\n\nTo best prepare your participation in {site}, we still need:\n\n%s\n\nYou can provide them in your partner area:\n\n%s\n\nThank you!\nThe {site} team\n",
	},
}
