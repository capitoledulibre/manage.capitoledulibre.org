package server

import (
	"io/fs"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"manage/web"
)

// TestTemplateLinksRoute checks that every absolute link and form action of
// the templates reaches a route: a renamed route otherwise leaves a form
// posting to a 404, which no handler test sees.
func TestTemplateLinksRoute(t *testing.T) {
	mux := newEnv(t).app.routes()

	tag := regexp.MustCompile(`<(?:a|form|button|div|section|li|tr|span)\b[^>]*>`)
	attr := regexp.MustCompile(`\s(action|href|hx-get|hx-post)="([^"]*)"`)
	branch := regexp.MustCompile(`\{\{if [^}]*\}\}(.*?)\{\{else\}\}(.*?)\{\{end\}\}`)
	// A template action stands for a query string when named so, for a
	// path segment after a slash, else for a suffix.
	query := regexp.MustCompile(`\{\{[^}]*Query\}\}`)
	segment := regexp.MustCompile(`/\{\{[^}]*\}\}`)
	suffix := regexp.MustCompile(`\{\{[^}]*\}\}`)

	err := fs.WalkDir(web.Templates, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		src, err := fs.ReadFile(web.Templates, path)
		if err != nil {
			return err
		}

		for _, el := range tag.FindAllString(string(src), -1) {
			for _, m := range attr.FindAllStringSubmatch(el, -1) {
				method := "GET"
				if m[1] == "hx-post" || m[1] == "action" && !strings.Contains(el, `method="get"`) {
					method = "POST"
				}

				urls := []string{m[2]}
				if b := branch.FindStringSubmatch(m[2]); b != nil {
					urls = []string{strings.Replace(m[2], b[0], b[1], 1), strings.Replace(m[2], b[0], b[2], 1)}
				}

				for _, u := range urls {
					// Links built from a variable ("{{$base}}/…") are not
					// ours to check.
					if strings.HasPrefix(u, "{{") {
						continue
					}

					u = suffix.ReplaceAllString(segment.ReplaceAllString(query.ReplaceAllString(u, ""), "/x"), "")
					u, _, _ = strings.Cut(u, "?")
					u, _, _ = strings.Cut(u, "#")

					// Nor are links to another site.
					if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
						continue
					}

					if _, pattern := mux.Handler(httptest.NewRequest(method, u, nil)); pattern == "" {
						t.Errorf("%s: %s %s reaches no route (%s)", path, method, u, m[2])
					}
				}
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
