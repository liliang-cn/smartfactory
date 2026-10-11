package web

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/extract"
)

// MaxUpload is the largest file the library takes.
const MaxUpload = 50 << 20

// UploadResult is what became of one file sent to the library.
type UploadResult struct {
	File    string         `json:"file"`
	OK      bool           `json:"ok"`
	Outcome agents.Outcome `json:"outcome,omitempty"`
	Doc     *agents.Doc    `json:"doc,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// libraryRoutes: documents as files — PDF, Word, Excel, CSV, Markdown, text in UTF-8 or GBK —
// uploaded by the company's administrators, read into passages, kept with every version, and
// downloadable again by its members.
func (s *Server) libraryRoutes(mux *http.ServeMux,
	company func(http.ResponseWriter, *http.Request) (string, bool),
	need func(http.ResponseWriter, *http.Request, string, auth.Role) (auth.Who, bool),
	found func(http.ResponseWriter, error, string) bool,
) {
	a := s.Agents
	// multipart/form-data: one or more "file" parts; "title" names the document when one file is sent
	mux.HandleFunc("POST /api/library/{company}/files", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		who, ok := need(w, r, c, auth.Admin)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4*MaxUpload)
		mr, err := r.MultipartReader()
		if err != nil {
			writeError(w, http.StatusBadRequest, "要用 multipart/form-data 上传，文件放在 file 字段")
			return
		}
		var title string
		var out []UploadResult
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("一次上传不能超过 %d MB", 4*MaxUpload>>20))
					return
				}
				writeError(w, http.StatusBadRequest, "上传没读完："+err.Error())
				return
			}
			switch part.FormName() {
			case "title":
				b, _ := io.ReadAll(io.LimitReader(part, 1024))
				title = strings.TrimSpace(string(b))
			case "file":
				out = append(out, s.upload(r, c, who, part, title))
			}
			part.Close()
		}
		if len(out) == 0 {
			writeError(w, http.StatusBadRequest, "没有收到文件（file 字段）")
			return
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /api/library/{company}/docs/{doc}/versions", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		list, err := a.Store.Versions(c, r.PathValue("doc"))
		if found(w, err, "文档") {
			writeJSON(w, list)
		}
	})
	// ?version=N for an earlier one; the original file as it was uploaded
	mux.HandleFunc("GET /api/library/{company}/docs/{doc}/file", func(w http.ResponseWriter, r *http.Request) {
		c, ok := company(w, r)
		if !ok {
			return
		}
		if _, ok := need(w, r, c, auth.Member); !ok {
			return
		}
		v, _ := strconv.Atoi(r.URL.Query().Get("version"))
		o, err := a.Store.File(c, r.PathValue("doc"), v)
		if !found(w, err, "版本") {
			return
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": o.Name}))
		if o.Path == "" {
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			io.WriteString(w, o.Text)
			return
		}
		f, err := os.Open(o.Path)
		if err != nil {
			writeError(w, http.StatusNotFound, "这个版本的文件不在了")
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(o.Name))); t != "" {
			w.Header().Set("Content-Type", t)
		}
		http.ServeContent(w, r, "", st.ModTime(), f)
	})
}

// upload reads one file part into the library.
func (s *Server) upload(r *http.Request, company string, who auth.Who, part io.Reader, title string) UploadResult {
	name := ""
	if p, ok := part.(interface{ FileName() string }); ok {
		name = filepath.Base(p.FileName())
	}
	res := UploadResult{File: name}
	if name == "" || name == "." {
		res.Error = "文件没有名字"
		return res
	}
	data, err := io.ReadAll(io.LimitReader(part, MaxUpload+1))
	if err != nil {
		res.Error = "没读完：" + err.Error()
		return res
	}
	if len(data) > MaxUpload {
		res.Error = fmt.Sprintf("超过 %d MB", MaxUpload>>20)
		return res
	}
	doc, err := extract.Extract(name, data)
	if err != nil {
		res.Error = strings.TrimPrefix(err.Error(), extract.ErrUnsupported.Error()+"：")
		return res
	}
	d, outcome, err := s.Agents.PutFile(r.Context(), company, agents.Upload{Title: title, File: name, Data: data, Doc: doc, By: who.Name})
	action := "doc.add"
	if outcome == agents.Updated {
		action = "doc.update"
	}
	s.Audit.Record(who, via(r), company, action, d.ID+" "+d.Title, fmt.Sprintf("%s 第 %d 版", name, d.Version), err)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK, res.Outcome, res.Doc = true, outcome, &d
	return res
}
