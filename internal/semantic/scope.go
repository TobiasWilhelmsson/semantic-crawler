package semantic

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

func validateURLScope(raw, host, prefix string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("source URL must be an absolute HTTP(S) URL without credentials")
	}
	if host != "" && !strings.EqualFold(parsed.Hostname(), host) {
		return fmt.Errorf("source URL host %q is outside configured scope", parsed.Hostname())
	}
	if prefix != "" {
		boundary := path.Clean("/" + strings.TrimPrefix(prefix, "/"))
		target := path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
		if boundary != "/" && target != boundary && !strings.HasPrefix(target, boundary+"/") {
			return fmt.Errorf("source URL path is outside configured scope %q", prefix)
		}
	}
	return nil
}
