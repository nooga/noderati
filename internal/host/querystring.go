package host

import (
	"net/url"
	"strings"

	"github.com/nooga/paserati/pkg/driver"
)

func declareQuerystring(p *driver.Paserati) {
	p.DeclareModule("querystring", func(m *driver.ModuleBuilder) {
		m.Function("parse", func(qs string) map[string]string {
			if strings.HasPrefix(qs, "?") {
				qs = qs[1:]
			}
			if qs == "" {
				return map[string]string{}
			}
			vals, err := url.ParseQuery(qs)
			if err != nil {
				return map[string]string{}
			}
			out := make(map[string]string, len(vals))
			for k, vs := range vals {
				if len(vs) > 0 {
					out[k] = vs[len(vs)-1]
				}
			}
			return out
		})
		m.Function("stringify", func(parts ...string) string {
			if len(parts) == 0 {
				return ""
			}
			pairs := make([]string, 0, len(parts)/2)
			for i := 0; i+1 < len(parts); i += 2 {
				pairs = append(pairs, url.QueryEscape(parts[i])+"="+url.QueryEscape(parts[i+1]))
			}
			return strings.Join(pairs, "&")
		})
		m.Function("escape", url.QueryEscape)
		m.Function("unescape", func(s string) string {
			if result, err := url.QueryUnescape(s); err == nil {
				return result
			}
			return s
		})
		m.Default(nil)
	})
	_ = p.DeclareModuleAlias("node:querystring", "querystring")
}
