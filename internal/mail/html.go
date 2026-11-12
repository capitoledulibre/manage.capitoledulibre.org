package mail

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"
)

// Brand is how the HTML version of an e-mail looks: the site's name, its
// address, its logo (an absolute URL, empty for none: e-mail clients do
// not show SVG) and its accent colour.
type Brand struct {
	Name    string
	URL     string
	LogoURL string
	Color   string
}

// HTML renders a plain-text body as the HTML version of an e-mail, so the
// bodies are written once, as text. Paragraphs are separated by blank
// lines; a paragraph that is a link alone is a button, the link shown
// below it for when the button does not work; lines starting with "•" or
// "-" are a list, the indented lines after one continuing its item; an
// indented paragraph (a slot, a quote) stands out in a box. Links and
// addresses anywhere are clickable.
func HTML(subject, body, lang string, b Brand) string {
	return HTMLWith(subject, body, lang, b, nil)
}

// HTMLWith is HTML, with decorations: wherever the text of a key shows
// (outside links), its value, trusted HTML such as an avatar, goes before
// it.
func HTMLWith(subject, body, lang string, b Brand, decor map[string]string) string {
	if b.Color == "" {
		b.Color = "#ea580c"
	}

	var out bytes.Buffer

	r := renderer{lang: langOr(lang), color: b.Color}

	for k, v := range decor {
		r.decor = append(r.decor, template.HTMLEscapeString(k), v+template.HTMLEscapeString(k))
	}

	err := layout.Execute(&out, map[string]any{
		"Lang": langOr(lang), "Subject": subject, "Brand": b, "Blocks": r.blocks(body),
	})
	if err != nil {
		return ""
	}

	return out.String()
}

func langOr(lang string) string {
	if lang == "en" {
		return "en"
	}

	return "fr"
}

// labels are the few words the HTML version adds to the text.
var labels = map[string][2]string{
	"button":   {"Ouvrir le lien", "Open the link"},
	"fallback": {"Si le bouton ne fonctionne pas, copiez ce lien :", "If the button does not work, copy this link:"},
}

// Inline styles: e-mail clients drop style sheets.
const (
	styleP      = "margin:0 0 16px;"
	styleLink   = "color:%s;text-decoration:underline;"
	styleBox    = "margin:0 0 16px;padding:12px 16px;background:#f9fafb;border-left:3px solid %s;border-radius:4px;"
	styleList   = "margin:0 0 16px;padding:0 0 0 20px;"
	styleItem   = "margin:0 0 10px;"
	styleButton = "display:inline-block;padding:12px 22px;background:%s;color:#ffffff;font-weight:600;text-decoration:none;border-radius:6px;"
	styleSmall  = "margin:0 0 16px;font-size:12px;line-height:1.5;color:#6b7280;word-break:break-all;"
)

// renderer turns the text of a body into HTML, in a language and a colour,
// with decorations: pairs of escaped text and the HTML replacing it.
type renderer struct {
	lang  string
	color string
	decor []string
}

func (r renderer) label(key string) string {
	if r.lang == "en" {
		return labels[key][1]
	}

	return labels[key][0]
}

func (r renderer) style(s string) string { return strings.ReplaceAll(s, "%s", r.color) }

func (r renderer) blocks(body string) []template.HTML {
	body = strings.ReplaceAll(body, "\r\n", "\n")

	var out []template.HTML

	for _, para := range splitParagraphs(body) {
		out = append(out, r.block(para))
	}

	return out
}

// splitParagraphs cuts a body at its blank lines.
func splitParagraphs(body string) [][]string {
	var (
		out [][]string
		cur []string
	)

	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}

			continue
		}

		cur = append(cur, strings.TrimRight(line, " \t"))
	}

	if len(cur) > 0 {
		out = append(out, cur)
	}

	return out
}

func (r renderer) block(lines []string) template.HTML {
	first := strings.TrimSpace(lines[0])

	switch {
	case len(lines) == 1 && isURL(first):
		u := template.HTMLEscapeString(first)

		return template.HTML(`<p style="margin:8px 0 12px;"><a href="` + u + `" style="` + r.style(styleButton) + `">` +
			template.HTMLEscapeString(r.label("button")) + `</a></p>` +
			`<p style="` + styleSmall + `">` + template.HTMLEscapeString(r.label("fallback")) + `<br>` +
			`<a href="` + u + `" style="` + r.style(styleLink) + `">` + u + `</a></p>`)
	case bullet(first) != "":
		return r.list(lines)
	case indented(lines):
		return template.HTML(`<div style="` + r.style(styleBox) + `">` + r.joinLines(lines, false) + `</div>`)
	default:
		return template.HTML(`<p style="` + styleP + `">` + r.joinLines(lines, false) + `</p>`)
	}
}

// list renders lines starting with a bullet as items, each following line
// that does not start with one continuing the item above it; the first
// line of an item with more is its title.
func (r renderer) list(lines []string) template.HTML {
	var (
		items [][]string
		head  []string
	)

	for _, line := range lines {
		if text := bullet(strings.TrimSpace(line)); text != "" {
			items = append(items, []string{text})

			continue
		}

		if len(items) == 0 {
			head = append(head, line)

			continue
		}

		items[len(items)-1] = append(items[len(items)-1], line)
	}

	var b strings.Builder

	if len(head) > 0 {
		b.WriteString(`<p style="` + styleP + `">` + r.joinLines(head, false) + `</p>`)
	}

	b.WriteString(`<ul style="` + styleList + `">`)

	for _, it := range items {
		b.WriteString(`<li style="` + styleItem + `">` + r.joinLines(it, len(it) > 1) + "</li>\n")
	}

	b.WriteString("</ul>")

	return template.HTML(b.String())
}

// bullet is the text of a list item line, "" for another line.
func bullet(line string) string {
	for _, p := range []string{"• ", "- "} {
		if strings.HasPrefix(line, p) {
			return strings.TrimSpace(strings.TrimPrefix(line, p))
		}
	}

	return ""
}

// indented tells whether every line of a paragraph starts with spaces.
func indented(lines []string) bool {
	for _, l := range lines {
		if !strings.HasPrefix(l, "  ") {
			return false
		}
	}

	return true
}

// joinLines renders the lines of a paragraph, the first one bold when
// title says so. A line that is a link alone reads "Open the link".
func (r renderer) joinLines(lines []string, title bool) string {
	parts := make([]string, len(lines))

	for i, l := range lines {
		l = strings.TrimSpace(l)

		switch {
		case isURL(l):
			parts[i] = `<a href="` + template.HTMLEscapeString(l) + `" style="` + r.style(styleLink) + `">` +
				template.HTMLEscapeString(r.label("button")) + ` →</a>`
		case i == 0 && title:
			parts[i] = "<strong>" + r.inline(l) + "</strong>"
		default:
			parts[i] = r.inline(l)
		}
	}

	return strings.Join(parts, "<br>\n")
}

var linkRe = regexp.MustCompile(`https?://[^\s<>"]+|[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// inline escapes a line and makes its links and addresses clickable; the
// text around them gets its decorations. A long link shows shortened; it
// leads to the whole of it.
func (r renderer) inline(s string) string {
	var b strings.Builder

	last := 0

	for _, m := range linkRe.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		// Punctuation closing a sentence is not part of the link.
		for end > start && strings.ContainsRune(".,;:!?)»", rune(s[end-1])) {
			end--
		}

		b.WriteString(r.text(s[last:start]))

		target, shown := s[start:end], s[start:end]
		if isURL(target) {
			shown = shorten(target)
		} else {
			target = "mailto:" + target
		}

		b.WriteString(`<a href="` + template.HTMLEscapeString(target) + `" style="` + r.style(styleLink) + `">` +
			template.HTMLEscapeString(shown) + `</a>`)

		last = end
	}

	b.WriteString(r.text(s[last:]))

	return b.String()
}

// text escapes text, then decorates it.
func (r renderer) text(s string) string {
	s = template.HTMLEscapeString(s)
	if len(r.decor) > 0 {
		s = strings.NewReplacer(r.decor...).Replace(s)
	}

	return s
}

func isURL(s string) bool {
	return (strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://")) && !strings.ContainsAny(s, " \t")
}

// shorten shows a link without its scheme, cut after 48 characters.
func shorten(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")

	if r := []rune(u); len(r) > 48 {
		return string(r[:47]) + "…"
	}

	return u
}

var layout = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<meta name="supported-color-schemes" content="light">
<title>{{.Subject}}</title>
</head>
<body style="margin:0;padding:0;background:#f3f4f6;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#f3f4f6;">
<tr><td align="center" style="padding:24px 12px;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:600px;background:#ffffff;border-radius:12px;border-top:4px solid {{.Brand.Color}};">
<tr><td style="padding:24px 32px 4px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
{{- if .Brand.LogoURL}}<img src="{{.Brand.LogoURL}}" alt="{{.Brand.Name}}" height="44" style="display:block;height:44px;width:auto;border:0;">
{{- else}}<span style="font-size:18px;font-weight:700;color:#111827;">{{.Brand.Name}}</span>{{end}}
</td></tr>
<tr><td style="padding:16px 32px 12px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.6;color:#111827;">
{{range .Blocks}}{{.}}
{{end -}}
</td></tr>
</table>
<p style="margin:16px 0 0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:12px;color:#6b7280;">
{{- .Brand.Name}}{{with .Brand.URL}} · <a href="{{.}}" style="color:#6b7280;">{{.}}</a>{{end}}</p>
</td></tr>
</table>
</body>
</html>
`))
