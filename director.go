package modeld

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/pkg/progress"
	"github.com/jmorganca/ollama/api"
	"github.com/yeahdongcn/modeld/server"
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

func writeSuccess(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_ = json.NewEncoder(w).Encode(v)
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
		return r.handleImageList(l, req, upstream)
	case match(`POST`, `^/images/create$`):
		return r.handleImagePull(l, req, upstream)
	case match(`DELETE`, `^/images/(.+)$`):
		return r.handleImageDelete(l, req, upstream)
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

func (r *RulesDirector) handleImageList(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Parse out query string to modify it
		var q = req.URL.Query()

		l.Printf("Query: %v", q)

		models := make([]api.ModelResponse, 0)
		manifestsPath, err := server.GetManifestPath()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		modelResponse := func(modelName string) (api.ModelResponse, error) {
			model, err := server.GetModel(modelName)
			if err != nil {
				return api.ModelResponse{}, err
			}

			modelDetails := api.ModelDetails{
				Format:            model.Config.ModelFormat,
				Family:            model.Config.ModelFamily,
				Families:          model.Config.ModelFamilies,
				ParameterSize:     model.Config.ModelType,
				QuantizationLevel: model.Config.FileType,
			}

			return api.ModelResponse{
				Model:   model.ShortName,
				Name:    model.ShortName,
				Size:    model.Size,
				Digest:  model.Digest,
				Details: modelDetails,
			}, nil
		}

		walkFunc := func(path string, info os.FileInfo, _ error) error {
			if !info.IsDir() {
				path, tag := filepath.Split(path)
				model := strings.Trim(strings.TrimPrefix(path, manifestsPath), string(os.PathSeparator))
				modelPath := strings.Join([]string{model, tag}, ":")
				canonicalModelPath := strings.ReplaceAll(modelPath, string(os.PathSeparator), "/")

				resp, err := modelResponse(canonicalModelPath)
				if err != nil {
					slog.Info(fmt.Sprintf("skipping file: %s", canonicalModelPath))
					// nolint: nilerr
					return nil
				}

				resp.ModifiedAt = info.ModTime()
				models = append(models, resp)
			}

			return nil
		}

		if err := filepath.Walk(manifestsPath, walkFunc); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		images := make([]image.Summary, 0)
		for _, model := range models {
			images = append(images, image.Summary{
				RepoTags: []string{model.Name},
				ID:       model.Digest,
				Size:     model.Size,
				Created:  model.ModifiedAt.Unix(),
			})
		}
		writeSuccess(w, images)
	})
}

func (r *RulesDirector) handleImagePull(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Parse out query string to modify it
		var q = req.URL.Query()

		regOpts := &server.RegistryOptions{
			// TODO: Make this configurable
			Insecure: true,
		}

		fn := func(r api.ProgressResponse) {
			progress := progress.Progress{
				Total: r.Total,
				ID:    r.Digest,
			}
			_ = json.NewEncoder(w).Encode(progress)
		}

		ctx, cancel := context.WithCancel(req.Context())
		defer cancel()

		model := fmt.Sprintf("%s:%s", q.Get("fromImage"), q.Get("tag"))
		if err := server.PullModel(ctx, model, regOpts, fn); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

func (r *RulesDirector) handleImageDelete(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		model := path.Base(req.URL.Path)
		if err := server.DeleteModel(model); err != nil {
			if os.IsNotExist(err) {
				writeError(w, fmt.Sprintf("model '%s' not found", model), http.StatusNotFound)
			} else {
				writeError(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		manifestsPath, err := server.GetManifestPath()
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := server.PruneDirectory(manifestsPath); err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		writeSuccess(w, []image.DeleteResponse{
			{
				Deleted: model,
			},
		})
	})
}

func (r *RulesDirector) handleContainerCreate(l socketproxy.Logger, req *http.Request, upstream http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Parse out query string to modify it
		var q = req.URL.Query()

		// Rebuild the query string ready to forward request
		req.URL.RawQuery = q.Encode()

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
