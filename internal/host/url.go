package host

import (
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/nooga/paserati/pkg/driver"
	"golang.org/x/net/idna"
)

// specialSchemes are the schemes WHATWG's URL origin algorithm treats as
// having a real (tuple) origin — everything else gets the opaque origin
// "null" (the literal string, matching real Node/browsers), not the
// scheme://host string a naive implementation might produce.
var specialSchemes = map[string]bool{
	"http": true, "https": true, "ws": true, "wss": true,
	"ftp": true, "file": true,
}

// jsURL is a read-only snapshot of a WHATWG URL, exposed to JS as the
// `URL` class. Every field is computed once at construction time (own
// data properties, per ModuleBuilder.Class/bindStructFields — there is
// no live getter/setter support, so mutating an instance's properties
// afterward doesn't recompute href the way real Node's URL does). No
// `.searchParams` property either — nothing needs a *live* link between
// a URL instance and a URLSearchParams yet (see urlsearchparams.go for
// the standalone `new URLSearchParams(...)` class itself, added
// 2026-09-02).
type jsURL struct {
	Href     string `json:"href"`
	Origin   string `json:"origin"`
	Protocol string `json:"protocol"`
	Username string `json:"username"`
	Password string `json:"password"`
	Host     string `json:"host"`
	Hostname string `json:"hostname"`
	Port     string `json:"port"`
	Pathname string `json:"pathname"`
	Search   string `json:"search"`
	Hash     string `json:"hash"`
}

func (u *jsURL) ToString() string { return u.Href }
func (u *jsURL) ToJSON() string   { return u.Href }

// newJSURL parses href as an absolute URL, WHATWG-style: an empty scheme
// (a relative or otherwise not-obviously-a-URL string) is a hard error,
// same as real `new URL(str)` without a base. This matters beyond
// correctness for its own sake — real packages (hosted-git-info's
// parse-url.js is what surfaced this) construct a URL specifically to
// detect malformed/non-URL input via the throw, e.g. to fall through to
// an scp-style-URL correction path. Go's net/url.Parse is far more
// permissive than WHATWG and rarely errors on its own; the empty-scheme
// check is what makes this throw where it needs to.
func newJSURL(href string) (*jsURL, error) {
	parsed, err := url.Parse(href)
	if err != nil {
		return nil, fmt.Errorf("Invalid URL: %s", href)
	}
	if parsed.Scheme == "" {
		return nil, fmt.Errorf("Invalid URL: %s", href)
	}

	protocol := parsed.Scheme + ":"
	origin := "null"
	if specialSchemes[parsed.Scheme] {
		origin = protocol + "//" + parsed.Host
	}
	search := ""
	if parsed.RawQuery != "" {
		search = "?" + parsed.RawQuery
	}
	hash := ""
	if parsed.Fragment != "" {
		hash = "#" + parsed.Fragment
	}
	password := ""
	if pw, ok := parsed.User.Password(); ok {
		password = pw
	}

	return &jsURL{
		Href:     parsed.String(),
		Origin:   origin,
		Protocol: protocol,
		Username: parsed.User.Username(),
		Password: password,
		Host:     parsed.Host,
		Hostname: parsed.Hostname(),
		Port:     parsed.Port(),
		Pathname: parsed.Path,
		Search:   search,
		Hash:     hash,
	}, nil
}

func declareURL(p *driver.Paserati) {
	p.DeclareModule("url", func(m *driver.ModuleBuilder) {
		m.Class("URL", &jsURL{}, newJSURL)
		m.Class("URLSearchParams", &urlSearchParams{}, newURLSearchParams)
		m.Function("fileURLToPath", func(fileURL string) (string, error) {
			u, err := url.Parse(fileURL)
			if err != nil {
				return "", err
			}
			if u.Scheme != "file" {
				return "", fmt.Errorf("fileURLToPath: must be a file URL")
			}
			return filepath.FromSlash(u.Path), nil
		})
		m.Function("pathToFileURL", func(p string) (string, error) {
			if !filepath.IsAbs(p) {
				abs, err := filepath.Abs(p)
				if err != nil {
					return "", err
				}
				p = abs
			}
			u := url.URL{
				Scheme: "file",
				Path:   filepath.ToSlash(p),
			}
			return u.String(), nil
		})
		m.Function("domainToASCII", func(domain string) (string, error) {
			return idna.ToASCII(domain)
		})
		m.Function("domainToUnicode", func(domain string) (string, error) {
			return idna.ToUnicode(domain)
		})
		m.Function("parse", func(href string) (map[string]string, error) {
			u, err := url.Parse(href)
			if err != nil {
				return nil, err
			}
			result := map[string]string{
				"hostname": u.Hostname(),
				"pathname": u.Path,
				"href":     u.String(),
				"host":     u.Host,
				"port":     u.Port(),
			}
			if u.Scheme != "" {
				result["protocol"] = u.Scheme + ":"
			} else {
				result["protocol"] = ""
			}
			if u.RawQuery != "" {
				result["search"] = "?" + u.RawQuery
			} else {
				result["search"] = ""
			}
			if u.Fragment != "" {
				result["hash"] = "#" + u.Fragment
			} else {
				result["hash"] = ""
			}
			return result, nil
		})
		m.Function("resolve", func(from, to string) (string, error) {
			base, err := url.Parse(from)
			if err != nil {
				return "", err
			}
			ref, err := url.Parse(to)
			if err != nil {
				return "", err
			}
			return base.ResolveReference(ref).String(), nil
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:url", "url")
}
