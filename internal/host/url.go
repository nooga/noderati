package host

import (
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/nooga/paserati/pkg/driver"
	"golang.org/x/net/idna"
)

func declareURL(p *driver.Paserati) {
	p.DeclareModule("url", func(m *driver.ModuleBuilder) {
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
