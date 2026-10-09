package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"

	"github.com/nektos/act/pkg/common"
)

type FileContainerResourceURL struct {
	FileContainerResourceURL string `json:"fileContainerResourceUrl"`
}

type NamedFileContainerResourceURL struct {
	Name                     string `json:"name"`
	FileContainerResourceURL string `json:"fileContainerResourceUrl"`
}

type NamedFileContainerResourceURLResponse struct {
	Count int                             `json:"count"`
	Value []NamedFileContainerResourceURL `json:"value"`
}

type ContainerItem struct {
	Path            string `json:"path"`
	ItemType        string `json:"itemType"`
	ContentLocation string `json:"contentLocation"`
}

type ContainerItemResponse struct {
	Value []ContainerItem `json:"value"`
}

type ResponseMessage struct {
	Message string `json:"message"`
}

type WritableFile interface {
	io.WriteCloser
}

type WriteFS interface {
	OpenWritable(name string) (WritableFile, error)
	OpenAppendable(name string) (WritableFile, error)
}

type readWriteFSImpl struct {
}

func (fwfs readWriteFSImpl) Open(name string) (fs.File, error) {
	return os.Open(name)
}

func (fwfs readWriteFSImpl) OpenWritable(name string) (WritableFile, error) {
	if err := os.MkdirAll(filepath.Dir(name), os.ModePerm); err != nil {
		return nil, err
	}
	return os.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
}

func (fwfs readWriteFSImpl) OpenAppendable(name string) (WritableFile, error) {
	if err := os.MkdirAll(filepath.Dir(name), os.ModePerm); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0o644)

	if err != nil {
		return nil, err
	}

	_, err = file.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	return file, nil
}

var gzipExtension = ".gz__"

func safeResolve(baseDir string, relPath string) string {
	return filepath.Join(baseDir, filepath.Clean(filepath.Join(string(os.PathSeparator), relPath)))
}

// requestBaseURL returns the externally visible scheme and host. Reverse proxies should
// set X-Forwarded-Proto and X-Forwarded-Host; without those headers we use the
// request's TLS state and Host header.
func requestBaseURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	if forwardedProto := strings.TrimSpace(strings.Split(req.Header.Get("X-Forwarded-Proto"), ",")[0]); forwardedProto == "http" || forwardedProto == "https" {
		scheme = forwardedProto
	}
	host := req.Host
	if forwardedHost := strings.TrimSpace(strings.Split(req.Header.Get("X-Forwarded-Host"), ",")[0]); forwardedHost != "" {
		host = forwardedHost
	}
	return scheme + "://" + host
}

func uploads(router *httprouter.Router, baseDir string, fsys WriteFS) {
	router.POST("/_apis/pipelines/workflows/:runId/artifacts", func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		runID := params.ByName("runId")

		json, err := json.Marshal(FileContainerResourceURL{
			FileContainerResourceURL: fmt.Sprintf("%s/upload/%s", requestBaseURL(req), runID),
		})
		if err != nil {
			panic(err)
		}

		_, err = w.Write(json)
		if err != nil {
			panic(err)
		}
	})

	router.PUT("/upload/:runId", func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		itemPath := req.URL.Query().Get("itemPath")
		runID := params.ByName("runId")

		if req.Header.Get("Content-Encoding") == "gzip" {
			itemPath += gzipExtension
		}

		safeRunPath := safeResolve(baseDir, runID)
		safePath := safeResolve(safeRunPath, itemPath)

		file, err := func() (WritableFile, error) {
			contentRange := req.Header.Get("Content-Range")
			if contentRange != "" && !strings.HasPrefix(contentRange, "bytes 0-") {
				return fsys.OpenAppendable(safePath)
			}
			return fsys.OpenWritable(safePath)
		}()

		if err != nil {
			panic(err)
		}
		defer file.Close()

		writer, ok := file.(io.Writer)
		if !ok {
			panic(errors.New("File is not writable"))
		}

		if req.Body == nil {
			panic(errors.New("No body given"))
		}

		_, err = io.Copy(writer, req.Body)
		if err != nil {
			panic(err)
		}

		json, err := json.Marshal(ResponseMessage{
			Message: "success",
		})
		if err != nil {
			panic(err)
		}

		_, err = w.Write(json)
		if err != nil {
			panic(err)
		}
	})

	router.PATCH("/_apis/pipelines/workflows/:runId/artifacts", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		json, err := json.Marshal(ResponseMessage{
			Message: "success",
		})
		if err != nil {
			panic(err)
		}

		_, err = w.Write(json)
		if err != nil {
			panic(err)
		}
	})
}

func downloads(router *httprouter.Router, baseDir string, fsys fs.FS) {
	router.GET("/_apis/pipelines/workflows/:runId/artifacts", func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		runID := params.ByName("runId")

		safePath := safeResolve(baseDir, runID)

		entries, err := fs.ReadDir(fsys, safePath)
		if err != nil {
			panic(err)
		}

		var list []NamedFileContainerResourceURL
		for _, entry := range entries {
			list = append(list, NamedFileContainerResourceURL{
				Name:                     entry.Name(),
				FileContainerResourceURL: fmt.Sprintf("%s/download/%s", requestBaseURL(req), runID),
			})
		}

		json, err := json.Marshal(NamedFileContainerResourceURLResponse{
			Count: len(list),
			Value: list,
		})
		if err != nil {
			panic(err)
		}

		_, err = w.Write(json)
		if err != nil {
			panic(err)
		}
	})

	router.GET("/download/:container", func(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
		container := params.ByName("container")
		itemPath := req.URL.Query().Get("itemPath")
		safePath := safeResolve(baseDir, filepath.Join(container, itemPath))

		var files []ContainerItem
		err := fs.WalkDir(fsys, safePath, func(path string, entry fs.DirEntry, _ error) error {
			if !entry.IsDir() {
				rel, err := filepath.Rel(safePath, path)
				if err != nil {
					panic(err)
				}

				// if it was upload as gzip
				rel = strings.TrimSuffix(rel, gzipExtension)
				path := filepath.Join(itemPath, rel)

				rel = filepath.ToSlash(rel)
				path = filepath.ToSlash(path)

				files = append(files, ContainerItem{
					Path:            path,
					ItemType:        "file",
					ContentLocation: fmt.Sprintf("%s/artifact/%s/%s/%s", requestBaseURL(req), container, itemPath, rel),
				})
			}
			return nil
		})
		if err != nil {
			panic(err)
		}

		json, err := json.Marshal(ContainerItemResponse{
			Value: files,
		})
		if err != nil {
			panic(err)
		}

		_, err = w.Write(json)
		if err != nil {
			panic(err)
		}
	})

	router.GET("/artifact/*path", func(w http.ResponseWriter, _ *http.Request, params httprouter.Params) {
		path := params.ByName("path")[1:]

		safePath := safeResolve(baseDir, path)

		file, err := fsys.Open(safePath)
		if err != nil {
			// try gzip file
			file, err = fsys.Open(safePath + gzipExtension)
			if err != nil {
				panic(err)
			}
			w.Header().Add("Content-Encoding", "gzip")
		}

		_, err = io.Copy(w, file)
		if err != nil {
			panic(err)
		}
	})
}

type localArtifactFile struct {
	Name         string
	RunID        string
	RelativePath string
	Size         int64
	ModTime      time.Time
}

// localArtifactsPage exposes a small browser UI for artifacts stored by this
// standalone server. It does not register remote files in GitHub's own artifact
// catalogue; it makes locally stored files discoverable and downloadable.
func localArtifactsPage(root string, w http.ResponseWriter, _ *http.Request) {
	files := make([]localArtifactFile, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		runID := ""
		if len(parts) > 1 {
			runID = parts[0]
		}
		files = append(files, localArtifactFile{
			Name:         strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			RunID:        runID,
			RelativePath: filepath.ToSlash(rel),
			Size:         info.Size(),
			ModTime:      info.ModTime(),
		})
		return nil
	})
	if err != nil {
		http.Error(w, "could not list local artifacts", http.StatusInternalServerError)
		return
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime.After(files[j].ModTime)
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Local Actions Artifacts</title><style>
:root{color-scheme:light dark}body{font:15px system-ui,-apple-system,sans-serif;max-width:1050px;margin:40px auto;padding:0 20px}h1{font-size:25px}p{opacity:.75}.path{font-family:ui-monospace,monospace;font-size:12px;overflow-wrap:anywhere}table{width:100%;border-collapse:collapse;margin-top:24px}th,td{text-align:left;padding:12px 10px;border-bottom:1px solid #8885}th{opacity:.7}a{color:inherit} .size{white-space:nowrap}@media(max-width:650px){.run{display:none}}
</style></head><body><h1>Locally stored workflow artifacts</h1><p>Files are stored on this runner. This page is separate from GitHub's built-in Actions → Artifacts list.</p>`)
	if len(files) == 0 {
		b.WriteString("<p>No ZIP artifacts found.</p>")
	} else {
		b.WriteString("<table><thead><tr><th>Artifact</th><th class=\"run\">Run key</th><th>Size</th><th>Updated</th></tr></thead><tbody>")
		for _, file := range files {
			b.WriteString("<tr><td><a href=\"/local-artifacts/download?path=")
			b.WriteString(template.HTMLEscapeString(url.QueryEscape(file.RelativePath)))
			b.WriteString("\">")
			b.WriteString(template.HTMLEscapeString(file.Name))
			b.WriteString("</a><div class=\"path\">")
			b.WriteString(template.HTMLEscapeString(file.RelativePath))
			b.WriteString("</div></td><td class=\"run\">")
			b.WriteString(template.HTMLEscapeString(file.RunID))
			b.WriteString("</td><td class=\"size\">")
			b.WriteString(fmt.Sprintf("%.2f MiB", float64(file.Size)/(1024*1024)))
			b.WriteString("</td><td>")
			b.WriteString(template.HTMLEscapeString(file.ModTime.Local().Format("2006-01-02 15:04:05")))
			b.WriteString("</td></tr>")
		}
		b.WriteString("</tbody></table>")
	}
	b.WriteString("</body></html>")
	_, _ = io.WriteString(w, b.String())
}

func downloadLocalArtifact(root string, w http.ResponseWriter, req *http.Request) {
	rel := filepath.Clean(filepath.FromSlash(req.URL.Query().Get("path")))
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		http.Error(w, "invalid artifact path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(root, rel)
	within, err := filepath.Rel(root, full)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(os.PathSeparator)) {
		http.Error(w, "invalid artifact path", http.StatusBadRequest)
		return
	}
	file, err := os.Open(full)
	if err != nil {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "artifact not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(full)}))
	http.ServeContent(w, req, filepath.Base(full), info.ModTime(), file)
}

func Serve(ctx context.Context, artifactPath string, addr string, port string) context.CancelFunc {
	serverContext, cancel := context.WithCancel(ctx)
	logger := common.Logger(serverContext)

	if artifactPath == "" {
		return cancel
	}

	absoluteArtifactPath, err := filepath.Abs(artifactPath)
	if err != nil {
		logger.Errorf("Could not resolve artifact storage path %q: %v", artifactPath, err)
		return cancel
	}
	artifactPath = absoluteArtifactPath

	router := httprouter.New()
	router.GET("/", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		localArtifactsPage(artifactPath, w, r)
	})
	router.GET("/artifacts", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		localArtifactsPage(artifactPath, w, r)
	})
	router.GET("/local-artifacts/download", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		downloadLocalArtifact(artifactPath, w, r)
	})

	logger.Infof("Artifacts base path: %s", artifactPath)
	fsys := readWriteFSImpl{}
	uploads(router, artifactPath, fsys)
	downloads(router, artifactPath, fsys)
	RoutesV4(router, artifactPath, fsys, fsys)

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", addr, port),
		ReadHeaderTimeout: 2 * time.Second,
		Handler:           router,
	}

	// run server
	go func() {
		logger.Infof("Start server on http://%s:%s", addr, port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal(err)
		}
	}()

	// wait for cancel to gracefully shutdown server
	go func() {
		<-serverContext.Done()

		if err := server.Shutdown(ctx); err != nil {
			logger.Errorf("Failed shutdown gracefully - force shutdown: %v", err)
			server.Close()
		}
	}()

	return cancel
}
