package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
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

func (fwfs readWriteFSImpl) RemoveAll(name string) error {
	return os.RemoveAll(name)
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


type localArtifactSummaryRow struct {
	Name         string
	RelativePath string
	SizeText     string
	Digest       string
	DigestFull   string
}

// localArtifactsSummaryPage serves the styled artifact table as a real HTML page.
// GitHub strips style attributes from job summaries, so the custom card must be
// rendered by the artifact server rather than embedded directly in GITHUB_STEP_SUMMARY.
func localArtifactsSummaryPage(root string, w http.ResponseWriter, req *http.Request) {
	runID := strings.TrimSpace(req.URL.Query().Get("runId"))
	if runID == "" {
		http.Error(w, "runId is required", http.StatusBadRequest)
		return
	}

	rows := make([]localArtifactSummaryRow, 0)
	err := filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".zip") {
			return nil
		}

		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		if len(parts) < 3 || parts[0] != runID {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		archive, err := os.Open(filePath)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, archive)
		closeErr := archive.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}

		digestFull := fmt.Sprintf("sha256:%x", hash.Sum(nil))
		digest := digestFull
		hexStart := strings.Index(digestFull, ":") + 1
		if len(digestFull)-hexStart > 8 {
			digest = digestFull[:hexStart+8] + "…"
		}

		rows = append(rows, localArtifactSummaryRow{
			Name:         strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			RelativePath: rel,
			SizeText:     formatLocalArtifactSize(info.Size()),
			Digest:       digest,
			DigestFull:   digestFull,
		})
		return nil
	})
	if err != nil {
		http.Error(w, "could not list local artifacts", http.StatusInternalServerError)
		return
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Artifacts</title></head><body>")
	b.WriteString("<div style=\"border: 1px solid #d0d7de; border-radius: 6px; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Helvetica, Arial, sans-serif; background-color: #ffffff; color: #24292f; max-width: 900px;\">")
	b.WriteString("<div style=\"padding: 16px;\">")
	b.WriteString("<h2 style=\"margin: 0; font-size: 16px; font-weight: 600;\">Artifacts</h2>")
	b.WriteString("<p style=\"margin: 4px 0 24px 0; font-size: 14px; color: #57606a;\">Produced during runtime</p>")
	b.WriteString("<table style=\"width: 100%; border-collapse: collapse; text-align: left; font-size: 14px;\">")
	b.WriteString("<thead><tr>")
	b.WriteString("<th style=\"padding: 8px 16px 16px 16px; color: #57606a; font-weight: 600; width: 45%;\">Name</th>")
	b.WriteString("<th style=\"padding: 8px 16px 16px 16px; color: #57606a; font-weight: 600; width: 20%;\">Size</th>")
	b.WriteString("<th style=\"padding: 8px 16px 16px 16px; color: #57606a; font-weight: 600;\">Digest</th>")
	b.WriteString("<th style=\"padding: 8px 16px 16px 16px;\"></th>")
	b.WriteString("</tr></thead><tbody>")

	if len(rows) == 0 {
		b.WriteString("<tr style=\"border-top: 1px solid #d0d7de;\"><td colspan=\"4\" style=\"padding: 16px; color: #57606a;\">No artifacts found for this run.</td></tr>")
	}

	packageIcon := "<svg width=\"16\" height=\"16\" viewBox=\"0 0 16 16\" fill=\"none\" stroke=\"currentColor\" stroke-width=\"1.5\" stroke-linecap=\"round\" stroke-linejoin=\"round\" style=\"color: #57606a; flex-shrink: 0; min-width: 16px;\"><path d=\"M1.75 4.75L8 1.5L14.25 4.75V11.25L8 14.5L1.75 11.25V4.75Z\" /><path d=\"M1.75 4.75L8 8L14.25 4.75\" /><path d=\"M8 8V14.5\" /></svg>"
	for _, row := range rows {
		href := "/local-artifacts/download?path=" + url.QueryEscape(row.RelativePath)
		escapedHref := template.HTMLEscapeString(href)
		escapedName := template.HTMLEscapeString(row.Name)
		escapedDigest := template.HTMLEscapeString(row.Digest)
		escapedDigestFull := template.HTMLEscapeString(row.DigestFull)

		b.WriteString("<tr style=\"border-top: 1px solid #d0d7de;\">")
		b.WriteString("<td style=\"padding: 16px; font-weight: 600;\"><div style=\"display: flex; align-items: center; gap: 8px;\">")
		b.WriteString("<a href=\"" + escapedHref + "\" style=\"color: #24292f; text-decoration: none; cursor: pointer;\">" + packageIcon + "</a>")
		b.WriteString("<a href=\"" + escapedHref + "\" style=\"color: #24292f; text-decoration: none; cursor: pointer; word-break: break-all;\">" + escapedName + "</a>")
		b.WriteString("</div></td>")
		b.WriteString("<td style=\"padding: 16px;\">" + template.HTMLEscapeString(row.SizeText) + "</td>")
		b.WriteString("<td style=\"padding: 16px;\"><div style=\"display: flex; align-items: center; gap: 8px;\">")
		b.WriteString("<span title=\"" + escapedDigestFull + "\" style=\"font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 13px;\">" + escapedDigest + "</span>")
		b.WriteString("<button type=\"button\" data-copy-digest=\"" + escapedDigestFull + "\" title=\"Copy digest\" aria-label=\"Copy digest\" style=\"border: 0; padding: 0; background: transparent; display: inline-flex; align-items: center; cursor: pointer;\"><img src=\"/local-artifacts/icon/copy.svg\" alt=\"Copy digest\" width=\"16\" height=\"16\" /></button>")
		b.WriteString("</div></td>")
		b.WriteString("<td style=\"padding: 16px; text-align: right; color: #57606a;\"><div style=\"display: flex; align-items: center; justify-content: flex-end; gap: 16px;\">")
		b.WriteString("<a href=\"" + escapedHref + "\" title=\"Download artifact\" style=\"cursor: pointer;\"><img src=\"/local-artifacts/icon/download.svg\" alt=\"Download\" width=\"16\" height=\"16\" /></a>")
		b.WriteString("<span title=\"Delete\" aria-label=\"Delete\"><img src=\"/local-artifacts/icon/trash.svg\" alt=\"Delete\" width=\"16\" height=\"16\" /></span>")
		b.WriteString("</div></td></tr>")
	}
	b.WriteString("</tbody></table></div></div>")
	b.WriteString("<script>document.addEventListener(\"click\",function(event){var button=event.target.closest(\"[data-copy-digest]\");if(!button)return;var digest=button.getAttribute(\"data-copy-digest\");if(navigator.clipboard&&navigator.clipboard.writeText){navigator.clipboard.writeText(digest).then(function(){button.title=\"Copied\";});}});</script>")
	b.WriteString("</body></html>")
	_, _ = io.WriteString(w, b.String())
}

func formatLocalArtifactSize(bytes int64) string {
	if bytes >= 1024*1024 {
		return fmt.Sprintf("%.2f MB", float64(bytes)/(1024*1024))
	}
	if bytes >= 1024 {
		return fmt.Sprintf("%.2f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%d B", bytes)
}

// localArtifactIcons are 16px SVGs used by the job summary and artifact page
// that the runner writes. GitHub renders summary images through its image proxy,
// which needs a public URL that answers with an image content type.
var localArtifactIcons = map[string]string{
	"package":  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="#57606a" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M1.75 4.75L8 1.5L14.25 4.75V11.25L8 14.5L1.75 11.25V4.75Z"/><path d="M1.75 4.75L8 8L14.25 4.75"/><path d="M8 8V14.5"/></svg>`,
	"copy":     `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16" fill="#0969da"><path d="M0 6.75C0 5.784.784 5 1.75 5h1.5a.75.75 0 0 1 0 1.5h-1.5a.25.25 0 0 0-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 0 0 .25-.25v-1.5a.75.75 0 0 1 1.5 0v1.5A1.75 1.75 0 0 1 9.25 16h-7.5A1.75 1.75 0 0 1 0 14.25Z"/><path d="M5 1.75C5 .784 5.784 0 6.75 0h7.5C15.216 0 16 .784 16 1.75v7.5A1.75 1.75 0 0 1 14.25 11h-7.5A1.75 1.75 0 0 1 5 9.25Zm1.75-.25a.25.25 0 0 0-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 0 0 .25-.25v-7.5a.25.25 0 0 0-.25-.25Z"/></svg>`,
	"download": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16" fill="#59636e"><path d="M2.75 14A1.75 1.75 0 0 1 1 12.25v-2.5a.75.75 0 0 1 1.5 0v2.5c0 .138.112.25.25.25h10.5a.25.25 0 0 0 .25-.25v-2.5a.75.75 0 0 1 1.5 0v2.5A1.75 1.75 0 0 1 13.25 14Z"/><path d="M7.25 7.689V2a.75.75 0 0 1 1.5 0v5.689l1.97-1.969a.749.749 0 1 1 1.06 1.06l-3.25 3.25a.749.749 0 0 1-1.06 0L4.22 6.78a.749.749 0 1 1 1.06-1.06l1.97 1.969Z"/></svg>`,
	"trash":    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16" fill="#59636e"><path d="M11 1.75V3h2.25a.75.75 0 0 1 0 1.5H2.75a.75.75 0 0 1 0-1.5H5V1.75C5 .784 5.784 0 6.75 0h2.5C10.216 0 11 .784 11 1.75ZM4.496 6.675l.66 6.6a.25.25 0 0 0 .249.225h5.19a.25.25 0 0 0 .249-.225l.66-6.6a.75.75 0 0 1 1.492.149l-.66 6.6A1.748 1.748 0 0 1 10.595 15h-5.19a1.75 1.75 0 0 1-1.741-1.575l-.66-6.6a.75.75 0 1 1 1.492-.15ZM6.5 1.75V3h3V1.75a.25.25 0 0 0-.25-.25h-2.5a.25.25 0 0 0-.25.25Z"/></svg>`,
}

func localArtifactIcon(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	svg, ok := localArtifactIcons[strings.TrimSuffix(ps.ByName("name"), ".svg")]
	if !ok {
		http.Error(w, "icon not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(svg))
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
	if errors.Is(err, fs.ErrNotExist) {
		// Runners that link with the raw workflow_run_backend_id (a GUID) do not match
		// the hashed run directory the v4 server stores artifacts under.
		if parts := strings.SplitN(filepath.ToSlash(rel), "/", 2); len(parts) == 2 && parts[0] != "" {
			alt := filepath.Join(root, fmt.Sprint(runKeyFromBackendID(parts[0])), filepath.FromSlash(parts[1]))
			if within, relErr := filepath.Rel(root, alt); relErr == nil && within != ".." && !strings.HasPrefix(within, ".."+string(os.PathSeparator)) {
				file, err = os.Open(alt)
				full = alt
			}
		}
	}
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
	router.GET("/local-artifacts/summary", func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
		localArtifactsSummaryPage(artifactPath, w, r)
	})
	router.GET("/local-artifacts/icon/:name", localArtifactIcon)

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
