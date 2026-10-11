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

	// Spaces of a person, linked from their menu.
	"space.partners":   {"Espace partenaire", "Partner area"},
	"space.volunteers": {"Espace bénévole", "Volunteer area"},

	// Volunteer space. French punctuation keeps a no-break space before
	// ":", "!" and "?".
	"vol.nav.poles":  {"Les pôles", "Teams"},
	"vol.nav.open":   {"Créneaux à pourvoir", "Open slots"},
	"vol.nav.me":     {"Mon planning", "My schedule"},
	"vol.breadcrumb": {"Fil d'Ariane", "Breadcrumb"},
	"vol.details":    {"Détails", "Details"},
	"vol.save":       {"Enregistrer", "Save"},
	"vol.copy":       {"Copier", "Copy"},
	"vol.copied":     {"Copié ✓", "Copied ✓"},
	"vol.or":         {" ou ", " or "},

	"vol.deadline_day":  {"%s inclus", "the end of %s"},
	"vol.deadline_time": {"%s à %s", "%s at %s"},
	"vol.slot_when":     {"%s de %s à %s", "%s from %s to %s"},

	"vol.managers.one":   {"Responsable\u00a0:", "Team lead:"},
	"vol.managers.other": {"Responsables\u00a0:", "Team leads:"},

	"vol.login.title":            {"Espace bénévole", "Volunteer area"},
	"vol.login.heading":          {"Espace bénévole", "Volunteer area"},
	"vol.login.heading_pole":     {"Connectez-vous pour rejoindre ce pôle", "Log in to join this team"},
	"vol.login.invited_to":       {"Vous avez été invité·e à rejoindre le pôle", "You have been invited to join the team"},
	"vol.login.intro":            {"Indiquez votre adresse e-mail\u00a0: nous vous envoyons un lien de connexion. Pas de mot de passe à retenir.", "Enter your email address: we will send you a login link. No password to remember."},
	"vol.login.placeholder":      {"vous@exemple.org", "you@example.org"},
	"vol.login.first_visit":      {"Première visite\u00a0? Votre compte est créé automatiquement, rien d'autre à remplir.", "First visit? Your account is created automatically, nothing else to fill in."},
	"vol.login.first_visit_pole": {"Première visite\u00a0? Votre compte est créé automatiquement, et vous arriverez directement sur ce pôle.", "First visit? Your account is created automatically, and you will land right on this team."},
	"vol.login.sent_body":        {"Un lien de connexion vient d'être envoyé à %s. Il est valable 30 minutes\u00a0; pensez à vérifier vos spams.", "A login link has just been sent to %s. It is valid for 30 minutes; please check your spam folder too."},
	"vol.login.wrong_address":    {"Ce n'est pas la bonne adresse\u00a0?", "Not the right address?"},

	"vol.welcome.title":         {"Bienvenue", "Welcome"},
	"vol.welcome.heading":       {"Bienvenue\u00a0!", "Welcome!"},
	"vol.welcome.intro":         {"Une seule question avant de continuer\u00a0: comment vous appelez-vous\u00a0? C'est ce que verront les responsables de pôle.", "Just one question before you go on: what is your name? This is what team leads will see."},
	"vol.welcome.phone_help":    {"(facultatif, visible des responsables uniquement, pour vous joindre le jour J)", "(optional, only team leads see it, to reach you on the day)"},
	"vol.welcome.continue":      {"Continuer", "Continue"},
	"vol.welcome.continue_pole": {"Continuer vers le pôle %s", "Continue to the %s team"},
	"vol.welcome.later":         {"Vous pourrez modifier ces informations plus tard, depuis «\u00a0Mon profil\u00a0».", "You can change this later from “My profile”."},

	"vol.profile.title":            {"Mon profil", "My profile"},
	"vol.profile.name":             {"Prénom et nom", "Full name"},
	"vol.profile.phone":            {"Téléphone", "Phone"},
	"vol.profile.phone_help":       {"Visible des responsables de vos pôles uniquement.", "Only the leads of your teams see it."},
	"vol.profile.share_phone":      {"Montrer mon téléphone aux bénévoles de mes pôles", "Show my phone number to the volunteers of my teams"},
	"vol.profile.share_phone_help": {"Il apparaît sur la page des créneaux, pour qu'on puisse vous joindre le jour J.", "It shows on the slot pages, so that people can reach you on the day."},
	"vol.profile.name_required":    {"Indiquez votre nom pour continuer.", "Please enter your name to continue."},

	"vol.no_event.title": {"Aucune édition en cours", "No edition open"},
	"vol.no_event.body":  {"Les inscriptions des bénévoles ne sont pas encore ouvertes. Revenez un peu plus tard\u00a0!", "Volunteer sign-ups are not open yet. Please come back a little later!"},

	"vol.home.title":                 {"Les pôles", "Teams"},
	"vol.home.hello":                 {"Bonjour %s", "Hello %s"},
	"vol.home.frozen":                {"Inscriptions closes", "Sign-ups closed"},
	"vol.home.draft":                 {"Brouillon, visible des organisateurs seulement", "Draft, only organisers see it"},
	"vol.home.my_count.one":          {"créneau à mon planning", "slot in my schedule"},
	"vol.home.my_count.other":        {"créneaux à mon planning", "slots in my schedule"},
	"vol.home.returning_title":       {"Content de vous revoir\u00a0!", "Good to see you again!"},
	"vol.home.returning_intro.one":   {"Pour %[2]s, vous étiez dans ce pôle. On repart ensemble\u00a0?", "For %[2]s, you were in this team. Shall we go again?"},
	"vol.home.returning_intro.other": {"Pour %[2]s, vous étiez dans ces pôles. On repart ensemble\u00a0?", "For %[2]s, you were in these teams. Shall we go again?"},
	"vol.home.join_again":            {"Rejoindre à nouveau", "Join again"},
	"vol.home.ask_named":             {"Sur invitation\u00a0: demandez à", "By invitation: ask"},
	"vol.home.ask_manager":           {"Sur invitation\u00a0: demandez au responsable.", "By invitation: ask the team lead."},
	"vol.home.returning_other":       {"Envie de changer\u00a0? Tous les pôles sont ci-dessous, et vous pouvez aussi voir", "Fancy a change? All the teams are below, and you can also see"},
	"vol.home.open_link":             {"où on a besoin de vous", "where you are needed"},
	"vol.home.steps_title":           {"Bienvenue\u00a0! Deux étapes et c'est fait", "Welcome! Two steps and you are done"},
	"vol.home.step1":                 {"Choisissez un pôle ci-dessous, selon ce que vous aimez faire. Pas de préférence\u00a0? Voyez directement", "Pick a team below, depending on what you like doing. No preference? See right away"},
	"vol.home.step2":                 {"Inscrivez-vous sur les créneaux qui vous arrangent, autant que vous voulez. Certains pôles sont sur invitation\u00a0: le responsable vous ajoute ou vous transmet un lien.", "Sign up for the slots that suit you, as many as you like. Some teams are by invitation: the lead adds you or sends you a link."},
	"vol.home.until":                 {"Vous pourrez revenir modifier vos créneaux à tout moment jusqu'au %s.", "You can come back and change your slots at any time until %s."},
	"vol.home.while_open":            {"Vous pourrez revenir modifier vos créneaux à tout moment, tant que les inscriptions sont ouvertes.", "You can come back and change your slots at any time while sign-ups are open."},
	"vol.home.upcoming":              {"Mes prochains créneaux", "My next slots"},
	"vol.home.all_planning":          {"Tout mon planning", "My whole schedule"},
	"vol.home.no_upcoming":           {"Vous n'êtes inscrit·e sur aucun créneau à venir. Choisissez un pôle ci-dessous, ou voyez", "You are not signed up for any upcoming slot. Pick a team below, or see"},
	"vol.home.poles":                 {"Les pôles", "Teams"},
	"vol.home.all_open":              {"Tous les créneaux à pourvoir", "All open slots"},

	"vol.pole.title":                 {"Pôle", "Team"},
	"vol.pole.everyone":              {"tous les bénévoles", "all volunteers"},
	"vol.pole.on_invitation":         {"sur invitation", "by invitation"},
	"vol.pole.restricted":            {"restreint", "restricted"},
	"vol.pole.member":                {"membre", "member"},
	"vol.pole.slots.one":             {"%d créneau", "%d slot"},
	"vol.pole.slots.other":           {"%d créneaux", "%d slots"},
	"vol.pole.missing.one":           {"il manque %d bénévole", "%d volunteer needed"},
	"vol.pole.missing.other":         {"il manque %d bénévoles", "%d volunteers needed"},
	"vol.pole.complete":              {"complet", "full"},
	"vol.pole.no_slots":              {"Aucun créneau publié pour le moment.", "No slot published yet."},
	"vol.pole.see_slots":             {"Voir les créneaux", "See the slots"},
	"vol.pole.join":                  {"Rejoindre ce pôle", "Join this team"},
	"vol.pole.ask_link":              {"Demandez le lien d'invitation au responsable.", "Ask the team lead for the invitation link."},
	"vol.pole.ask_add":               {"Le responsable vous ajoute sur demande.", "The team lead adds you on request."},
	"vol.pole.not_member":            {"Vous n'êtes pas encore dans ce pôle", "You are not in this team yet"},
	"vol.pole.open_help":             {"Inscrivez-vous directement sur un créneau ci-dessous\u00a0: vous rejoindrez le pôle au passage. Vous pouvez aussi le rejoindre d'abord pour suivre ses messages, et le quitter à tout moment.", "Sign up for a slot below right away: you will join the team on the way. You can also join it first to follow its messages, and leave it at any time."},
	"vol.pole.private_help":          {"Ce pôle se rejoint par un lien d'invitation\u00a0: demandez-le au responsable.", "This team is joined through an invitation link: ask the team lead for it."},
	"vol.pole.invitation_help":       {"Ce pôle est sur invitation\u00a0: le responsable vous ajoutera sur demande.", "This team is by invitation: the team lead will add you on request."},
	"vol.pole.summary_missing.one":   {"Il manque %d bénévole", "%d volunteer still needed"},
	"vol.pole.summary_missing.other": {"Il manque %d bénévoles", "%d volunteers still needed"},
	"vol.pole.summary_full":          {"Tous les créneaux sont pourvus", "All slots are filled"},
	"vol.pole.summary_mine.one":      {"vous êtes inscrit·e sur %d créneau", "you are signed up for %d slot"},
	"vol.pole.summary_mine.other":    {"vous êtes inscrit·e sur %d créneaux", "you are signed up for %d slots"},
	"vol.pole.locked":                {"Les inscriptions sont closes\u00a0: vos créneaux ne peuvent plus être modifiés. Contactez un responsable si besoin.", "Sign-ups are closed: your slots can no longer be changed. Contact a team lead if needed."},
	"vol.pole.deadline":              {"Inscriptions et modifications possibles jusqu'au %s.", "Sign-ups and changes are possible until %s."},
	"vol.pole.filters":               {"Filtrer les créneaux", "Filter the slots"},
	"vol.pole.all_locations":         {"Tous les emplacements", "All places"},
	"vol.pole.only_open":             {"À pourvoir seulement", "Open only"},
	"vol.pole.all_filled":            {"Tous les créneaux sont pourvus, merci\u00a0!", "All slots are filled, thank you!"},
	"vol.pole.none_yet":              {"Aucun créneau n'a encore été publié dans ce pôle.", "No slot has been published in this team yet."},
	"vol.pole.leave_title":           {"Quitter ce pôle", "Leave this team"},
	"vol.pole.leave_help":            {"Vous ne suivrez plus ses messages.", "You will no longer follow its messages."},
	"vol.pole.leave_help_mine.one":   {"Vous serez désinscrit·e de votre créneau dans ce pôle.", "You will be taken off your slot in this team."},
	"vol.pole.leave_help_mine.other": {"Vous serez désinscrit·e de vos %d créneaux dans ce pôle.", "You will be taken off your %d slots in this team."},
	"vol.pole.leave_confirm":         {"Quitter ce pôle et annuler vos créneaux\u00a0?", "Leave this team and cancel your slots?"},
	"vol.pole.leave":                 {"Quitter le pôle", "Leave the team"},

	"vol.join.title":   {"Rejoindre un pôle", "Join a team"},
	"vol.join.invited": {"Vous êtes invité·e à rejoindre ce pôle. Vous choisirez ensuite vos créneaux.", "You are invited to join this team. You will then pick your slots."},
	"vol.join.submit":  {"Rejoindre le pôle", "Join the team"},
	"vol.join.invalid": {"Ce lien d'invitation n'est pas valable. Demandez-en un nouveau au responsable du pôle.", "This invitation link is not valid. Ask the team lead for a new one."},

	"vol.slot.title":             {"Créneau", "Slot"},
	"vol.slot.signed_up":         {"Inscrit·e", "Signed up"},
	"vol.slot.signup":            {"Je m'inscris", "Sign me up"},
	"vol.slot.unsignup":          {"Me désinscrire", "Cancel my sign-up"},
	"vol.slot.closed":            {"Inscriptions closes", "Sign-ups closed"},
	"vol.slot.invitation_only":   {"Sur invitation", "By invitation"},
	"vol.slot.full":              {"Complet", "Full"},
	"vol.slot.waitlist_join":     {"Me prévenir si une place se libère", "Tell me if a place frees up"},
	"vol.slot.waitlist_leave":    {"Ne plus attendre", "Stop waiting"},
	"vol.slot.tasks_done.one":    {"%[2]d/%[1]d tâche faite", "%[2]d/%[1]d task done"},
	"vol.slot.tasks_done.other":  {"%[2]d/%[1]d tâches faites", "%[2]d/%[1]d tasks done"},
	"vol.slot.filled_sr.one":     {"%[1]d bénévole inscrit·e sur %[2]d souhaité·es", "%[1]d volunteer signed up out of %[2]d needed"},
	"vol.slot.filled_sr.other":   {"%[1]d bénévoles inscrit·es sur %[2]d souhaité·es", "%[1]d volunteers signed up out of %[2]d needed"},
	"vol.slot.volunteers":        {"Bénévoles (%d/%d)", "Volunteers (%d/%d)"},
	"vol.slot.nobody":            {"Personne pour l'instant.", "Nobody yet."},
	"vol.slot.places_left.one":   {"Encore %d place.", "%d place left."},
	"vol.slot.places_left.other": {"Encore %d places.", "%d places left."},
	"vol.slot.contacts":          {"Une question, un empêchement\u00a0?", "A question, something came up?"},
	"vol.slot.pole_manager":      {"responsable du pôle", "team lead"},

	"vol.tasks.title":          {"Tâches", "Tasks"},
	"vol.tasks.none":           {"Aucune tâche pour ce créneau.", "No task for this slot."},
	"vol.tasks.toggle":         {"Cocher", "Tick"},
	"vol.tasks.done_by":        {"fait par %s à %s", "done by %s at %s"},
	"vol.tasks.signup_to_tick": {"Inscrivez-vous sur le créneau pour pouvoir cocher les tâches.", "Sign up for the slot to tick the tasks."},
	"vol.tasks.announce":       {"Tâches\u00a0: %d sur %d faites.", "Tasks: %d of %d done."},

	"vol.waitlist.title": {"En liste d'attente", "On the waiting list"},
	"vol.waitlist.help":  {"Vous recevrez un e-mail dès qu'une place se libère\u00a0: premier arrivé, premier servi.", "You will get an email as soon as a place frees up: first come, first served."},
	"vol.waitlist.badge": {"En liste d'attente", "On the waiting list"},

	"vol.overlap.title":      {"Ce créneau en chevauche un autre", "This slot overlaps another one"},
	"vol.overlap.intro":      {"Ce créneau chevauche un autre de vos créneaux\u00a0:", "This slot overlaps another of your slots:"},
	"vol.overlap.page_intro": {"Le créneau %s – %s se superpose à\u00a0:", "The %s – %s slot overlaps:"},
	"vol.overlap.help":       {"Vous pouvez tout de même vous inscrire si vous savez que vous serez présent·e sur les deux.", "You can still sign up if you know you will be there for both."},
	"vol.overlap.confirm":    {"M'inscrire quand même", "Sign me up anyway"},
	"vol.overlap.announce":   {"Attention\u00a0: ce créneau chevauche un créneau où vous êtes déjà inscrit·e. Confirmez pour vous inscrire quand même.", "Careful: this slot overlaps one you are already signed up for. Confirm to sign up anyway."},

	"vol.open.title":    {"Créneaux à pourvoir", "Open slots"},
	"vol.open.heading":  {"Où a-t-on besoin de vous\u00a0?", "Where are you needed?"},
	"vol.open.intro":    {"%s · les créneaux qui attendent encore des bénévoles, tous pôles confondus", "%s · the slots still waiting for volunteers, all teams together"},
	"vol.open.help":     {"Inscrivez-vous directement\u00a0: si le créneau appartient à un pôle dont vous ne faites pas encore partie, vous le rejoignez au passage.", "Sign up right away: if the slot belongs to a team you are not in yet, you join it on the way."},
	"vol.open.by_day":   {"Filtrer par jour", "Filter by day"},
	"vol.open.all_days": {"Tous les jours", "All days"},
	"vol.open.none":     {"Tous les créneaux ouverts sont pourvus pour le moment, merci\u00a0!", "All open slots are filled for now, thank you!"},

	"vol.me.title":                  {"Mon planning", "My schedule"},
	"vol.me.count.one":              {"%d créneau", "%d slot"},
	"vol.me.count.other":            {"%d créneaux", "%d slots"},
	"vol.me.today":                  {"aujourd'hui", "today"},
	"vol.me.empty":                  {"Vous n'êtes inscrit·e sur aucun créneau pour l'instant. Voyez", "You are not signed up for any slot yet. See"},
	"vol.me.calendar_title":         {"Ajouter mon planning à mon agenda", "Add my schedule to my calendar"},
	"vol.me.calendar_sub":           {"se met à jour tout seul", "it updates itself"},
	"vol.me.calendar_help":          {"Ajoutez ce lien dans votre agenda (Google Agenda, Apple Calendrier, Thunderbird…) comme «\u00a0calendrier par abonnement\u00a0». Vos créneaux y apparaîtront, et toute modification suivra sans rien retélécharger.", "Add this link to your calendar (Google Calendar, Apple Calendar, Thunderbird…) as a “subscribed calendar”. Your slots will show up there, and every change will follow without downloading anything again."},
	"vol.me.calendar_label":         {"Lien d'abonnement à votre planning", "Subscription link to your schedule"},
	"vol.me.calendar_private":       {"Ce lien est personnel et donne accès à votre planning\u00a0: ne le partagez pas.", "This link is personal and gives access to your schedule: do not share it."},
	"vol.me.calendar_reset":         {"Régénérer le lien", "Generate a new link"},
	"vol.me.calendar_reset_confirm": {"Générer un nouveau lien\u00a0? Les agendas déjà abonnés cesseront de se mettre à jour tant qu'ils n'auront pas le nouveau lien.", "Generate a new link? Calendars already subscribed will stop updating until they get the new link."},
	"vol.me.calendar_download":      {"Sans abonnement, vous pouvez aussi télécharger le fichier une fois\u00a0; il ne suivra pas les changements.", "Without subscribing, you can also download the file once; it will not follow changes."},
	"vol.me.calendar_file":          {"Télécharger le fichier .ics", "Download the .ics file"},

	"vol.done.signup":        {"Inscription enregistrée\u00a0: %s, %s.", "Signed up: %s, %s."},
	"vol.done.joined_signup": {"Vous avez rejoint le pôle «\u00a0%s\u00a0». Inscription enregistrée\u00a0: %s.", "You joined the “%s” team. Signed up: %s."},
	"vol.done.unsignup":      {"Désinscription enregistrée\u00a0: %s. Vous pouvez vous réinscrire sur ce créneau.", "Sign-up cancelled: %s. You can sign up for this slot again."},
	"vol.done.waitlist":      {"Vous serez prévenu·e par e-mail si une place se libère sur ce créneau.", "You will get an email if a place frees up on this slot."},
	"vol.done.waitlist_left": {"Vous ne serez plus prévenu·e pour ce créneau.", "You will no longer be told about this slot."},

	"vol.err.invitation_only":      {"Ce pôle est sur invitation\u00a0: demandez au responsable de vous ajouter.", "This team is by invitation: ask the team lead to add you."},
	"vol.err.invitation_only_pole": {"Le pôle «\u00a0%s\u00a0» est sur invitation\u00a0: un responsable doit vous ajouter.", "The “%s” team is by invitation: a team lead has to add you."},
	"vol.err.bad_invite":           {"Ce lien d'invitation n'est pas valable pour le pôle «\u00a0%s\u00a0». Demandez-en un nouveau au responsable.", "This invitation link is not valid for the “%s” team. Ask the team lead for a new one."},
	"vol.err.locked":               {"Les inscriptions sont closes sur ce créneau.", "Sign-ups are closed on this slot."},
	"vol.err.locked_unsignup":      {"Les inscriptions sont closes\u00a0: contactez un responsable pour vous désengager.", "Sign-ups are closed: contact a team lead to withdraw."},
	"vol.err.full":                 {"Ce créneau est déjà complet\u00a0: personne d'autre n'est attendu.", "This slot is already full: nobody else is expected."},
	"vol.err.join_first":           {"Rejoignez d'abord le pôle.", "Join the team first."},
	"vol.err.closed":               {"Les inscriptions à cette édition sont fermées.", "Sign-ups to this edition are closed."},
	"vol.err.default_pole":         {"Ce pôle regroupe tous les bénévoles et ne peut pas être quitté.", "This team gathers all volunteers and cannot be left."},

	"flash.vol.joined":         {"Vous avez rejoint le pôle «\u00a0%s\u00a0». Choisissez vos créneaux\u00a0!", "You joined the “%s” team. Pick your slots!"},
	"flash.vol.left":           {"Vous avez quitté le pôle «\u00a0%s\u00a0».", "You left the “%s” team."},
	"flash.vol.profile_saved":  {"Profil enregistré.", "Profile saved."},
	"flash.vol.calendar_reset": {"Nouveau lien d'abonnement généré. L'ancien ne fonctionne plus\u00a0: réabonnez vos agendas.", "New subscription link generated. The old one no longer works: subscribe your calendars again."},

	// Emails. Bodies are plain text.
	"mail.login.subject": {"Votre lien de connexion — {site}", "Your login link — {site}"},
	"mail.login.body": {
		"Bonjour,\n\nVoici votre lien de connexion à {site} :\n\n%s\n\nCe lien est valable %d minutes et ne peut être utilisé qu'une fois.\nSi vous n'êtes pas à l'origine de cette demande, ignorez ce message.\n\nL'équipe {site}\n",
		"Hello,\n\nHere is your login link to {site}:\n\n%s\n\nThis link is valid for %d minutes and can only be used once.\nIf you did not request it, just ignore this email.\n\nThe {site} team\n",
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

	// Volunteer notifications. Bodies are plain text, wrapped by
	// mail.vol.greeting and mail.vol.signature.
	"mail.vol.greeting":                 {"Bonjour %s,", "Hello %s,"},
	"mail.vol.signature":                {"Votre planning\u00a0: %s\n\nMerci pour votre aide\u00a0!\nL'équipe {site}\n", "Your schedule: %s\n\nThank you for your help!\nThe {site} team\n"},
	"mail.vol.freed.subject":            {"Une place s'est libérée\u00a0: %s", "A place freed up: %s"},
	"mail.vol.freed.body":               {"Une place vient de se libérer sur un créneau que vous attendiez\u00a0:\n\n  %s\n\nPremier arrivé, premier servi\u00a0: %s\n\nSi vous ne le prenez pas, vous n'avez rien à faire\u00a0; nous vous préviendrons à la prochaine place libre.", "A place has just freed up on a slot you were waiting for:\n\n  %s\n\nFirst come, first served: %s\n\nIf you do not take it, there is nothing to do; we will tell you at the next free place."},
	"mail.vol.late.subject":             {"Désistement de dernière minute\u00a0: %s", "Last-minute withdrawal: %s"},
	"mail.vol.late.body":                {"%s vient de se désinscrire d'un créneau qui commence dans moins de 48 heures\u00a0:\n\n%s\nS'il y avait une liste d'attente, elle a été prévenue\u00a0; sinon, il faudra peut-être trouver quelqu'un d'autre.", "%s has just withdrawn from a slot starting in less than 48 hours:\n\n%s\nIf there was a waiting list, it has been told; otherwise someone else may have to be found."},
	"mail.vol.slot_deleted.subject":     {"Créneau annulé\u00a0: %s", "Slot cancelled: %s"},
	"mail.vol.slot_deleted.body":        {"Le créneau sur lequel vous étiez inscrit·e a été supprimé\u00a0:\n\n  %s\n\nVous n'avez donc plus rien à faire à ce moment-là. N'hésitez pas à choisir un autre créneau.", "The slot you were signed up for has been removed:\n\n  %s\n\nSo you have nothing to do at that time. Feel free to pick another slot."},
	"mail.vol.signup_removed.subject":   {"Inscription annulée\u00a0: %s", "Sign-up cancelled: %s"},
	"mail.vol.signup_removed.body":      {"Un responsable a annulé votre inscription sur ce créneau\u00a0:\n\n  %s\n\nVous restez membre du pôle «\u00a0%s\u00a0» et pouvez vous inscrire sur un autre créneau\u00a0: %s", "A team lead cancelled your sign-up for this slot:\n\n  %s\n\nYou are still a member of the “%s” team and can sign up for another slot: %s"},
	"mail.vol.pole_removed.subject":     {"Retrait du pôle %s", "Removed from the %s team"},
	"mail.vol.pole_removed.body":        {"Un responsable vous a retiré·e du pôle «\u00a0%s\u00a0». Vous pouvez toujours rejoindre un autre pôle depuis l'accueil.", "A team lead removed you from the “%s” team. You can still join another team from the home page."},
	"mail.vol.pole_removed.slots.one":   {"Votre inscription sur %d créneau de ce pôle a été annulée.", "Your sign-up for %d slot of this team was cancelled."},
	"mail.vol.pole_removed.slots.other": {"Vos %d inscriptions sur les créneaux de ce pôle ont été annulées.", "Your %d sign-ups for slots of this team were cancelled."},
	"mail.vol.slot_changed.subject":     {"Créneau modifié\u00a0: %s", "Slot changed: %s"},
	"mail.vol.slot_changed.body":        {"Un créneau sur lequel vous êtes inscrit·e a changé\u00a0:\n\n  Avant\u00a0:     %s\n  Désormais\u00a0: %s\n\nVous restez inscrit·e. Si le nouvel horaire ne vous convient plus, vous pouvez vous désinscrire\u00a0: %s", "A slot you are signed up for has changed:\n\n  Before: %s\n  Now:    %s\n\nYou are still signed up. If the new time no longer suits you, you can withdraw: %s"},
	"mail.vol.message.subject":          {"Message\u00a0: %s", "Message: %s"},
	"mail.vol.message.body":             {"Un message vient d'être publié pour %s\u00a0:\n\n%s\n\n%s", "A message has just been posted for %s:\n\n%s\n\n%s"},
}
