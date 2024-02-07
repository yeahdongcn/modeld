package modeld

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/yeahdongcn/modeld/socketproxy"
)

var (
	versionRegex = regexp.MustCompile(`^/v\d\.\d+\b`)
)

type RulesDirector struct {
	Client *http.Client
}

func writeError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": msg,
	})
}

func (r *RulesDirector) Direct(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	var match = func(method string, pattern string) bool {
		if method != "*" && method != req.Method {
			return false
		}
		path := req.URL.Path
		if versionRegex.MatchString(path) {
			path = versionRegex.ReplaceAllString(path, "")
		}
		re := regexp.MustCompile(pattern)
		return re.MatchString(path)
	}

	var errorHandler = func(msg string, code int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			l.Printf("Handler returned error %q", msg)
			writeError(w, msg, code)
		})
	}

	switch {
	case match(`GET`, `^/(_ping|version|info)$`):
		return upstream
	case match(`GET`, `^/events$`):
		return upstream

	// Container related endpoints
	case match(`POST`, `^/containers/create$`):
		return r.handleContainerCreate(l, req, upstream)
	case match(`POST`, `^/containers/prune$`):
		return upstream
	case match(`GET`, `^/containers/json$`):
		return upstream
	case match(`*`, `^/(containers|exec)/(\w+)\b`):
		return upstream

	// Build related endpoints
	case match(`POST`, `^/build$`):
		return r.handleBuild(l, req, upstream)

	// Image related endpoints
	case match(`GET`, `^/images/json$`):
		return upstream
	case match(`POST`, `^/images/create$`):
		return upstream
	case match(`POST`, `^/images/(create|search|get|load)$`):
		break
	case match(`POST`, `^/images/prune$`):
		return upstream
	case match(`*`, `^/images/(\w+)\b`):
		return upstream

	// Network related endpoints
	case match(`GET`, `^/networks$`):
		return upstream
	case match(`POST`, `^/networks/create$`):
		return r.handleNetworkCreate(l, req, upstream)
	case match(`POST`, `^/networks/prune$`):
		return upstream
	case match(`DELETE`, `^/networks/(.+)$`):
		return r.handleNetworkDelete(l, req, upstream)
	case match(`GET`, `^/networks/(.+)$`),
		match(`POST`, `^/networks/(.+)/(connect|disconnect)$`):
		return upstream

	// Volumes related endpoints
	case match(`GET`, `^/volumes$`):
		return upstream
	case match(`POST`, `^/volumes/create$`):
		return upstream
	case match(`POST`, `^/volumes/prune$`):
		return upstream
	case match(`GET`, `^/volumes/([-\w]+)$`), match(`DELETE`, `^/volumes/(-\w+)$`):
		return upstream

	}

	return errorHandler(req.Method+" "+req.URL.Path+" not implemented yet", http.StatusNotImplemented)
}

func (r *RulesDirector) handleContainerCreate(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		upstream.ServeHTTP(w, req)
	})
}

func (r *RulesDirector) handleNetworkCreate(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Parse out query string to modify it
		var q = req.URL.Query()

		// Rebuild the query string ready to forward request
		req.URL.RawQuery = q.Encode()

		// Do the network creation
		upstream.ServeHTTP(w, req)
	})
}

func (r *RulesDirector) handleNetworkDelete(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Parse out query string to modify it
		var q = req.URL.Query()

		// Rebuild the query string ready to forward request
		req.URL.RawQuery = q.Encode()

		// Do the network delete
		upstream.ServeHTTP(w, req)
	})
}

func (r *RulesDirector) handleBuild(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Parse out query string to modify it
		var q = req.URL.Query()

		// Rebuild the query string ready to forward request
		req.URL.RawQuery = q.Encode()

		upstream.ServeHTTP(w, req)
	})
}
